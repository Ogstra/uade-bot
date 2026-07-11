import { UserRecordSchema } from '../schemas.js';
import logger from '../logger.js';

/**
 * @param {object | undefined} row
 * @returns {import('zod').infer<typeof UserRecordSchema> | null}
 */
function rowToUser(row) {
  if (!row) {
    return null;
  }

  return UserRecordSchema.parse({
    discordUserId: row.discord_user_id,
    pauseReason: row.pause_reason,
    pauseUntil: row.pause_until,
    backoffAttempt: row.backoff_attempt,
    createdAt: row.created_at,
    updatedAt: row.updated_at,
  });
}

/**
 * Inserts a new `users` row for `discordUserId` (unpaused, backoffAttempt 0)
 * or, if the row already exists, only bumps `updated_at`. Returns the
 * resulting validated row.
 *
 * @param {import('better-sqlite3').Database} db
 * @param {string} discordUserId
 * @returns {import('zod').infer<typeof UserRecordSchema>}
 */
export function upsertUser(db, discordUserId) {
  const now = Date.now();

  db.prepare(
    `INSERT INTO users (discord_user_id, backoff_attempt, created_at, updated_at)
     VALUES (?, 0, ?, ?)
     ON CONFLICT(discord_user_id) DO UPDATE SET updated_at = excluded.updated_at`,
  ).run(discordUserId, now, now);

  logger.info({ event: 'user_upsert', discordUserId }, 'User upserted');

  return getUser(db, discordUserId);
}

/**
 * @param {import('better-sqlite3').Database} db
 * @param {string} discordUserId
 * @returns {import('zod').infer<typeof UserRecordSchema> | null}
 */
export function getUser(db, discordUserId) {
  const row = db.prepare('SELECT * FROM users WHERE discord_user_id = ?').get(discordUserId);
  return rowToUser(row);
}

/**
 * @param {import('better-sqlite3').Database} db
 * @param {string} discordUserId
 * @param {{ pauseReason: string | null, pauseUntil: number | null, backoffAttempt: number }} state
 * @returns {import('zod').infer<typeof UserRecordSchema>}
 */
export function updateAccountPauseState(db, discordUserId, { pauseReason, pauseUntil, backoffAttempt }) {
  const now = Date.now();

  db.prepare(
    `UPDATE users
     SET pause_reason = ?, pause_until = ?, backoff_attempt = ?, updated_at = ?
     WHERE discord_user_id = ?`,
  ).run(pauseReason, pauseUntil, backoffAttempt, now, discordUserId);

  logger.info({ event: 'user_pause_state_update', discordUserId }, 'Account pause state updated');

  return getUser(db, discordUserId);
}
