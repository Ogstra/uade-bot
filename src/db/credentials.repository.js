import { EncryptedCredentialsSchema } from '../schemas.js';
import logger from '../logger.js';

// This module never imports src/crypto/credentials-crypto.js. It stores and
// returns ciphertext/iv/authTag fields only — encryption/decryption is a
// separate concern kept entirely out of the repository layer (CRED-03).

/**
 * @param {object | undefined} row
 * @returns {import('zod').infer<typeof EncryptedCredentialsSchema> | null}
 */
function rowToCredentials(row) {
  if (!row) {
    return null;
  }

  return EncryptedCredentialsSchema.parse({
    discordUserId: row.discord_user_id,
    ciphertext: row.ciphertext,
    iv: row.iv,
    authTag: row.auth_tag,
    updatedAt: row.updated_at,
  });
}

/**
 * Inserts or replaces the encrypted-credentials row for `discordUserId`.
 * Ciphertext-only — never decrypts, never accepts plaintext.
 *
 * @param {import('better-sqlite3').Database} db
 * @param {{ discordUserId: string, ciphertext: string, iv: string, authTag: string }} params
 * @returns {import('zod').infer<typeof EncryptedCredentialsSchema>}
 */
export function upsertCredentials(db, { discordUserId, ciphertext, iv, authTag }) {
  const now = Date.now();

  db.prepare(
    `INSERT INTO credentials (discord_user_id, ciphertext, iv, auth_tag, updated_at)
     VALUES (?, ?, ?, ?, ?)
     ON CONFLICT(discord_user_id) DO UPDATE SET
       ciphertext = excluded.ciphertext,
       iv = excluded.iv,
       auth_tag = excluded.auth_tag,
       updated_at = excluded.updated_at`,
  ).run(discordUserId, ciphertext, iv, authTag, now);

  logger.info({ event: 'credentials_upsert', discordUserId }, 'Encrypted credentials upserted');

  return getCredentials(db, discordUserId);
}

/**
 * @param {import('better-sqlite3').Database} db
 * @param {string} discordUserId
 * @returns {import('zod').infer<typeof EncryptedCredentialsSchema> | null}
 */
export function getCredentials(db, discordUserId) {
  const row = db.prepare('SELECT * FROM credentials WHERE discord_user_id = ?').get(discordUserId);
  return rowToCredentials(row);
}
