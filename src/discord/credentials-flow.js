import { getCredentials, upsertCredentials } from '../db/credentials.repository.js';
import { upsertUser } from '../db/users.repository.js';
import { encryptCredentials, decryptCredentials } from '../crypto/credentials-crypto.js';
import { loadEnv } from '../config/env.js';
import logger from '../logger.js';

const DEFAULT_TIMEOUT_MS = 120000;

async function ask(dm, message, { timeoutMs = DEFAULT_TIMEOUT_MS } = {}) {
  await dm.send(message);
  const collected = await dm.awaitMessages({ max: 1, time: timeoutMs });
  const response = collected.first();
  if (!response?.content) {
    throw new Error('credential_prompt_timeout');
  }
  return response.content.trim();
}

export async function collectCredentialValues(dm, options = {}) {
  const uadeUsername = await ask(dm, 'Mandame tu usuario de UADE.', options);
  const uadePassword = await ask(dm, 'Mandame tu password de UADE.', options);
  const uadeStartUrl = await ask(dm, 'Mandame el link de inscripcion de UADE.', options);
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

export async function runFullCredentialOnboarding(interaction, { db, env } = {}) {
  let dm;
  try {
    dm = await interaction.user.createDM();
  } catch (err) {
    logger.warn({ event: 'credential_dm_open_failed', discordUserId: interaction.user.id }, 'Could not open credential DM');
    return {
      ok: false,
      message: 'No pude abrirte DM. Habilita mensajes privados del servidor y volve a intentar.',
    };
  }

  try {
    const values = await collectCredentialValues(dm);
    const resolvedEnv = env ?? loadEnv();
    saveCredentialValues(db, {
      discordUserId: interaction.user.id,
      masterKey: resolvedEnv.CREDENTIALS_MASTER_KEY,
      values,
    });
    return { ok: true, message: 'Credenciales guardadas.' };
  } catch (err) {
    logger.warn(
      { event: 'credential_onboarding_failed', discordUserId: interaction.user.id, message: err.message },
      'Credential onboarding failed',
    );
    return {
      ok: false,
      message: 'No pude completar la carga de credenciales. Volve a intentar con /credenciales.',
    };
  }
}

export async function runCredentialRotation(interaction, { db, env } = {}) {
  let dm;
  try {
    dm = await interaction.user.createDM();
  } catch (err) {
    logger.warn({ event: 'credential_dm_open_failed', discordUserId: interaction.user.id }, 'Could not open credential DM');
    return {
      ok: false,
      message: 'No pude abrirte DM. Habilita mensajes privados del servidor y volve a intentar.',
    };
  }

  const mode = interaction.options.getString('modo') ?? 'todo';
  try {
    const resolvedEnv = env ?? loadEnv();
    if (mode === 'usuario_password') {
      const uadeUsername = await ask(dm, 'Mandame tu nuevo usuario de UADE.');
      const uadePassword = await ask(dm, 'Mandame tu nuevo password de UADE.');
      rotateCredentialValues(db, {
        discordUserId: interaction.user.id,
        masterKey: resolvedEnv.CREDENTIALS_MASTER_KEY,
        updates: { uadeUsername, uadePassword },
      });
    } else if (mode === 'link') {
      const uadeStartUrl = await ask(dm, 'Mandame el nuevo link de inscripcion de UADE.');
      rotateCredentialValues(db, {
        discordUserId: interaction.user.id,
        masterKey: resolvedEnv.CREDENTIALS_MASTER_KEY,
        updates: { uadeStartUrl },
      });
    } else {
      const values = await collectCredentialValues(dm);
      saveCredentialValues(db, {
        discordUserId: interaction.user.id,
        masterKey: resolvedEnv.CREDENTIALS_MASTER_KEY,
        values,
      });
    }

    return { ok: true, message: mode === 'todo' ? 'Credenciales guardadas.' : 'Credenciales actualizadas.' };
  } catch (err) {
    logger.warn(
      { event: 'credential_rotation_failed', discordUserId: interaction.user.id, mode, message: err.message },
      'Credential rotation failed',
    );
    return {
      ok: false,
      message: 'No pude actualizar tus credenciales. Volve a intentar.',
    };
  }
}
