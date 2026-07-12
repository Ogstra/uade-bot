import { getCredentials, upsertCredentials } from '../db/credentials.repository.js';
import { upsertUser } from '../db/users.repository.js';
import { encryptCredentials, decryptCredentials } from '../crypto/credentials-crypto.js';
import { loadEnv } from '../config/env.js';
import { getBrowser, withPlainContext } from '../automation/browser.js';
import { obtainStartUrl } from '../automation/sso-link.js';
import logger from '../logger.js';
import {
  credentialOnboardingFailedMessage,
  credentialPrompts,
  credentialRotationFailedMessage,
  credentialsSavedMessage,
  credentialsUpdatedMessage,
  dmUnavailableMessage,
} from './messages.js';

const DEFAULT_TIMEOUT_MS = 120000;

/**
 * Fires off a shared-browser launch in the background right after
 * credentials are saved/rotated, so the account's first real search
 * doesn't pay Chromium's cold-start latency on top of its own postback
 * settle wait. Not awaited -- must never delay the Discord reply. The
 * browser closes itself on the next idle scheduler tick (queue.js) if
 * nothing ends up polling it, so no explicit close-after-N-seconds timer
 * is needed here.
 */
function warmBrowser(getBrowserFn) {
  getBrowserFn().catch((err) => {
    logger.error(
      { event: 'browser_warm_failed', message: err.message },
      'Failed to pre-warm the shared browser after credential submission',
    );
  });
}

/**
 * Fires an immediate poll for every active job belonging to `discordUserId`
 * right after their credentials are saved/rotated, instead of leaving them
 * to wait up to POLL_INTERVAL_MS for the next scheduled tick -- same
 * immediacy `/reanudar`'s `onJobResumed` already gives a single job.
 * Fire-and-forget, same reasoning as `warmBrowser`: must never delay the
 * Discord reply, and a failure here shouldn't fail the credential save
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
 * Fase 3.1: the bot tries to obtain the inscripción link itself first,
 * automating the Microsoft/Azure AD login with the credentials just
 * collected -- the user is only asked to paste a link manually as a
 * fallback, when that automated attempt can't complete (typically: the
 * account has MFA/2FA, which this bot deliberately never tries to bypass).
 *
 * @param {import('discord.js').DMChannel} dm
 * @param {{ userId: string, username: string, password: string, withPlainContextFn?: typeof withPlainContext, obtainStartUrlFn?: typeof obtainStartUrl }} options
 * @returns {Promise<string>}
 */
async function obtainOrAskStartUrl(
  dm,
  { userId, username, password, withPlainContextFn = withPlainContext, obtainStartUrlFn = obtainStartUrl },
) {
  const result = await withPlainContextFn((context) => obtainStartUrlFn(context, { username, password }));
  if (result.status === 'success') {
    return result.startUrl;
  }
  return ask(dm, credentialPrompts.startUrl, { userId });
}

export async function collectCredentialValues(dm, options = {}) {
  const uadeUsername = await ask(dm, credentialPrompts.username, options);
  const uadePassword = await ask(dm, credentialPrompts.password, options);
  const uadeStartUrl = await obtainOrAskStartUrl(dm, { ...options, username: uadeUsername, password: uadePassword });
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
    getBrowserFn = getBrowser,
    withPlainContextFn = withPlainContext,
    obtainStartUrlFn = obtainStartUrl,
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
    const values = await collectCredentialValues(dm, { userId: interaction.user.id, withPlainContextFn, obtainStartUrlFn });
    const resolvedEnv = env ?? loadEnv();
    saveCredentialValues(db, {
      discordUserId: interaction.user.id,
      masterKey: resolvedEnv.CREDENTIALS_MASTER_KEY,
      values,
    });
    warmBrowser(getBrowserFn);
    pollActiveJobsNow(onCredentialsUpdated, interaction.user.id);
    return { ok: true, message: credentialsSavedMessage() };
  } catch (err) {
    logger.warn(
      { event: 'credential_onboarding_failed', discordUserId: interaction.user.id, message: err.message },
      'Credential onboarding failed',
    );
    return {
      ok: false,
      message: credentialOnboardingFailedMessage(),
    };
  }
}

export async function runCredentialRotation(
  interaction,
  {
    db,
    env,
    getBrowserFn = getBrowser,
    withPlainContextFn = withPlainContext,
    obtainStartUrlFn = obtainStartUrl,
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
      // Fase 3.1: re-obtain the link too when username/password change --
      // otherwise an account that never had a link stored (or whose old one
      // no longer matches the new credentials) silently ends up with a
      // missing/stale uadeStartUrl (confirmed live 2026-07-12: navigation_failed
      // on every subsequent poll). Same auto-obtain-first, ask-as-fallback
      // path as full onboarding.
      const uadeStartUrl = await obtainOrAskStartUrl(dm, {
        userId: interaction.user.id,
        username: uadeUsername,
        password: uadePassword,
        withPlainContextFn,
        obtainStartUrlFn,
      });
      rotateCredentialValues(db, {
        discordUserId: interaction.user.id,
        masterKey: resolvedEnv.CREDENTIALS_MASTER_KEY,
        updates: { uadeUsername, uadePassword, uadeStartUrl },
      });
    } else if (mode === 'link') {
      const uadeStartUrl = await ask(dm, credentialPrompts.newStartUrl, { userId: interaction.user.id });
      rotateCredentialValues(db, {
        discordUserId: interaction.user.id,
        masterKey: resolvedEnv.CREDENTIALS_MASTER_KEY,
        updates: { uadeStartUrl },
      });
    } else {
      const values = await collectCredentialValues(dm, { userId: interaction.user.id, withPlainContextFn, obtainStartUrlFn });
      saveCredentialValues(db, {
        discordUserId: interaction.user.id,
        masterKey: resolvedEnv.CREDENTIALS_MASTER_KEY,
        values,
      });
    }

    warmBrowser(getBrowserFn);
    pollActiveJobsNow(onCredentialsUpdated, interaction.user.id);
    return { ok: true, message: mode === 'todo' ? credentialsSavedMessage() : credentialsUpdatedMessage() };
  } catch (err) {
    logger.warn(
      { event: 'credential_rotation_failed', discordUserId: interaction.user.id, mode, message: err.message },
      'Credential rotation failed',
    );
    return {
      ok: false,
      message: credentialRotationFailedMessage(),
    };
  }
}
