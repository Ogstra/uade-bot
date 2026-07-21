import { getCredentials, upsertCredentials } from '../db/credentials.repository.js';
import { upsertUser } from '../db/users.repository.js';
import { encryptCredentials, decryptCredentials } from '../crypto/credentials-crypto.js';
import { loadEnv } from '../config/env.js';
import logger from '../logger.js';
import {
  credentialOnboardingFailedMessage,
  credentialPrompts,
  credentialRotationFailedMessage,
  credentialsSavedMessage,
  credentialsUpdatedMessage,
  dmUnavailableMessage,
  invalidStartUrlMessage,
} from './messages.js';

const DEFAULT_TIMEOUT_MS = 120000;
const INSCRIPCION_HOST = 'inscripcionespia.uade.edu.ar';

/**
 * Fires an immediate poll for every active job belonging to `discordUserId`
 * right after their credentials are saved/rotated, instead of leaving them
 * to wait up to POLL_INTERVAL_MS for the next scheduled tick -- same
 * immediacy `/reanudar`'s `onJobResumed` already gives a single job.
 * Fire-and-forget: it must never delay the Discord reply, and a failure
 * here shouldn't fail the credential save
 * itself (the jobs simply get polled on the next normal tick instead).
 */
function pollActiveJobsNow(onCredentialsUpdated, discordUserId) {
  if (!onCredentialsUpdated) {
    return;
  }
  Promise.resolve(onCredentialsUpdated(discordUserId)).catch((err) => {
    logger.error(
      { event: 'credentials_updated_poll_failed', discordUserId, message: err.message },
      'Failed to trigger an immediate poll after a credential update',
    );
  });
}

/**
 * @param {import('discord.js').DMChannel} dm
 * @param {string} message
 * @param {{ timeoutMs?: number, userId: string }} options - `userId` is
 *   required: without a filter, `awaitMessages` collects ANY new message in
 *   the DM channel, including the prompt this function itself just sent via
 *   `dm.send()` -- that self-collection resolved instantly with the bot's
 *   own prompt text as the "reply", never actually waiting for the human
 *   (confirmed live 2026-07-12: every prompt fired back-to-back with no
 *   real wait).
 */
async function ask(dm, message, { timeoutMs = DEFAULT_TIMEOUT_MS, userId } = {}) {
  await dm.send(message);
  const collected = await dm.awaitMessages({
    max: 1,
    time: timeoutMs,
    filter: (m) => m.author?.id === userId,
  });
  const response = collected.first();
  if (!response?.content) {
    throw new Error('credential_prompt_timeout');
  }
  return response.content.trim();
}

/**
 * Same as `ask()`, but for a manually-pasted inscripción link specifically:
 * rejects (throws `invalid_start_url_link`, never persists anything) a reply
 * that isn't a real `inscripcionespia.uade.edu.ar` URL carrying a `param`
 * query parameter -- confirmed live 2026-07-13 that an unvalidated paste
 * (wrong domain, a non-link, or plain garbage) used to save straight through
 * to `uadeStartUrl`, after which every poll failed with `navigation_failed`
 * forever: transient `search_failed` outcomes don't pause an account, so the
 * account was stuck silently broken with no pause, no DM, and no self-healing.
 * Rejecting the bad link before it's ever saved is the narrow fix: it stops
 * the broken state from being created in the first place, without changing
 * what transient search failures do elsewhere.
 *
 * @param {import('discord.js').DMChannel} dm
 * @param {string} message
 * @param {{ timeoutMs?: number, userId: string }} options
 * @returns {Promise<string>}
 */
async function askStartUrl(dm, message, options) {
  const value = await ask(dm, message, options);
  if (!isValidStartUrl(value)) {
    throw new Error('invalid_start_url_link');
  }
  return value;
}

/**
 * Phase 3.2 hard boundary: the app runtime is browserless. The bot never
 * opens Playwright/Chromium to automate Microsoft SSO during onboarding or
 * rotation. Users paste the UADE inscripción link manually, and background
 * polling consumes it with the HTTP-only engine.
 *
 * @param {import('discord.js').DMChannel} dm
 * @param {{ userId: string }} options
 * @returns {Promise<string>}
 */
async function obtainOrAskStartUrl(dm, { userId }) {
  return askStartUrl(dm, credentialPrompts.startUrl, { userId });
}

/**
 * True only for a syntactically valid inscripción URL whose host is exactly
 * the HTTP-search target and which carries the opaque `param` query value.
 *
 * Kept in this module so credential onboarding can validate manual links
 * without importing the deleted Playwright SSO module.
 *
 * @param {unknown} candidate
 * @returns {boolean}
 */
export function isValidStartUrl(candidate) {
  if (typeof candidate !== 'string' || candidate.length === 0) {
    return false;
  }
  let parsed;
  try {
    parsed = new URL(candidate);
  } catch {
    return false;
  }
  return parsed.host === INSCRIPCION_HOST && parsed.searchParams.has('param');
}

export async function collectCredentialValues(dm, options = {}) {
  const uadeUsername = await ask(dm, credentialPrompts.username, options);
  const uadePassword = await ask(dm, credentialPrompts.password, options);
  const uadeStartUrl = await obtainOrAskStartUrl(dm, options);
  return { uadeUsername, uadePassword, uadeStartUrl };
}

export function saveCredentialValues(db, { discordUserId, masterKey, values }) {
  upsertUser(db, discordUserId);
  const encrypted = encryptCredentials(masterKey, discordUserId, values);
  return upsertCredentials(db, { discordUserId, ...encrypted });
}

export function rotateCredentialValues(db, { discordUserId, masterKey, updates }) {
  const existing = getCredentials(db, discordUserId);
  if (!existing) {
    if (!updates.uadeUsername || !updates.uadePassword || !updates.uadeStartUrl) {
      throw new Error('credentials_missing_for_partial_rotation');
    }
    return saveCredentialValues(db, {
      discordUserId,
      masterKey,
      values: {
        uadeUsername: updates.uadeUsername,
        uadePassword: updates.uadePassword,
        uadeStartUrl: updates.uadeStartUrl,
      },
    });
  }

  const current = decryptCredentials(masterKey, discordUserId, existing);
  return saveCredentialValues(db, {
    discordUserId,
    masterKey,
    values: { ...current, ...updates },
  });
}

export async function runFullCredentialOnboarding(
  interaction,
  {
    db,
    env,
    onCredentialsUpdated,
  } = {},
) {
  let dm;
  try {
    dm = await interaction.user.createDM();
  } catch (err) {
    logger.warn({ event: 'credential_dm_open_failed', discordUserId: interaction.user.id }, 'Could not open credential DM');
    return {
      ok: false,
      message: dmUnavailableMessage(),
    };
  }

  try {
    const values = await collectCredentialValues(dm, { userId: interaction.user.id });
    const resolvedEnv = env ?? loadEnv();
    saveCredentialValues(db, {
      discordUserId: interaction.user.id,
      masterKey: resolvedEnv.CREDENTIALS_MASTER_KEY,
      values,
    });
    pollActiveJobsNow(onCredentialsUpdated, interaction.user.id);
    return { ok: true, message: credentialsSavedMessage() };
  } catch (err) {
    logger.warn(
      { event: 'credential_onboarding_failed', discordUserId: interaction.user.id, message: err.message },
      'Credential onboarding failed',
    );
    return {
      ok: false,
      message: err.message === 'invalid_start_url_link' ? invalidStartUrlMessage() : credentialOnboardingFailedMessage(),
    };
  }
}

export async function runCredentialRotation(
  interaction,
  {
    db,
    env,
    onCredentialsUpdated,
  } = {},
) {
  let dm;
  try {
    dm = await interaction.user.createDM();
  } catch (err) {
    logger.warn({ event: 'credential_dm_open_failed', discordUserId: interaction.user.id }, 'Could not open credential DM');
    return {
      ok: false,
      message: dmUnavailableMessage(),
    };
  }

  const mode = interaction.options.getString('modo') ?? 'todo';
  try {
    const resolvedEnv = env ?? loadEnv();
    if (mode === 'usuario_password') {
      const uadeUsername = await ask(dm, credentialPrompts.newUsername, { userId: interaction.user.id });
      const uadePassword = await ask(dm, credentialPrompts.newPassword, { userId: interaction.user.id });
      // Username/password rotations also require a fresh manual link because
      // the runtime intentionally has no browser-based Microsoft relink path.
      const uadeStartUrl = await obtainOrAskStartUrl(dm, { userId: interaction.user.id });
      rotateCredentialValues(db, {
        discordUserId: interaction.user.id,
        masterKey: resolvedEnv.CREDENTIALS_MASTER_KEY,
        updates: { uadeUsername, uadePassword, uadeStartUrl },
      });
    } else if (mode === 'link') {
      const uadeStartUrl = await askStartUrl(dm, credentialPrompts.newStartUrl, { userId: interaction.user.id });
      rotateCredentialValues(db, {
        discordUserId: interaction.user.id,
        masterKey: resolvedEnv.CREDENTIALS_MASTER_KEY,
        updates: { uadeStartUrl },
      });
    } else {
      const values = await collectCredentialValues(dm, { userId: interaction.user.id });
      saveCredentialValues(db, {
        discordUserId: interaction.user.id,
        masterKey: resolvedEnv.CREDENTIALS_MASTER_KEY,
        values,
      });
    }

    pollActiveJobsNow(onCredentialsUpdated, interaction.user.id);
    return { ok: true, message: mode === 'todo' ? credentialsSavedMessage() : credentialsUpdatedMessage() };
  } catch (err) {
    logger.warn(
      { event: 'credential_rotation_failed', discordUserId: interaction.user.id, mode, message: err.message },
      'Credential rotation failed',
    );
    return {
      ok: false,
      message: err.message === 'invalid_start_url_link' ? invalidStartUrlMessage() : credentialRotationFailedMessage(),
    };
  }
}
