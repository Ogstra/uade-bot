import { getCredentials, upsertCredentials } from '../db/credentials.repository.js';
import { upsertUser } from '../db/users.repository.js';
import { encryptCredentials, decryptCredentials } from '../crypto/credentials-crypto.js';
import { loadEnv } from '../config/env.js';
import { getBrowser } from '../automation/browser.js';
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

export async function collectCredentialValues(dm, options = {}) {
  const uadeUsername = await ask(dm, credentialPrompts.username, options);
  const uadePassword = await ask(dm, credentialPrompts.password, options);
  const uadeStartUrl = await ask(dm, credentialPrompts.startUrl, options);
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

export async function runFullCredentialOnboarding(interaction, { db, env, getBrowserFn = getBrowser } = {}) {
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
    warmBrowser(getBrowserFn);
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

export async function runCredentialRotation(interaction, { db, env, getBrowserFn = getBrowser } = {}) {
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
      rotateCredentialValues(db, {
        discordUserId: interaction.user.id,
        masterKey: resolvedEnv.CREDENTIALS_MASTER_KEY,
        updates: { uadeUsername, uadePassword },
      });
    } else if (mode === 'link') {
      const uadeStartUrl = await ask(dm, credentialPrompts.newStartUrl, { userId: interaction.user.id });
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

    warmBrowser(getBrowserFn);
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
