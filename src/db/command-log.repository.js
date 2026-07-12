/**
 * @param {import('better-sqlite3').Database} db
 * @param {{ discordUserId: string, commandName: string, guildId?: string | null }} entry
 */
export function logCommandUsage(db, { discordUserId, commandName, guildId = null }) {
  db.prepare(
    `INSERT INTO command_log (discord_user_id, command_name, guild_id, created_at)
     VALUES (?, ?, ?, ?)`,
  ).run(discordUserId, commandName, guildId, Date.now());
}

/**
 * @param {import('better-sqlite3').Database} db
 * @param {string} discordUserId
 * @param {{ limit?: number }} [options]
 * @returns {{ commandName: string, guildId: string | null, createdAt: number }[]}
 */
export function listCommandUsageByUser(db, discordUserId, { limit = 50 } = {}) {
  const rows = db
    .prepare(
      `SELECT command_name, guild_id, created_at FROM command_log
       WHERE discord_user_id = ?
       ORDER BY id DESC
       LIMIT ?`,
    )
    .all(discordUserId, limit);

  return rows.map((row) => ({
    commandName: row.command_name,
    guildId: row.guild_id,
    createdAt: row.created_at,
  }));
}

/**
 * Usage counts per command name, most-used first -- admin stats.
 *
 * @param {import('better-sqlite3').Database} db
 * @param {{ limit?: number }} [options]
 * @returns {{ commandName: string, count: number }[]}
 */
export function countCommandUsageByCommand(db, { limit = 10 } = {}) {
  const rows = db
    .prepare(
      `SELECT command_name, COUNT(*) AS count FROM command_log
       GROUP BY command_name
       ORDER BY count DESC
       LIMIT ?`,
    )
    .all(limit);

  return rows.map((row) => ({ commandName: row.command_name, count: row.count }));
}

/**
 * @param {import('better-sqlite3').Database} db
 * @returns {number}
 */
export function countTotalCommandUsage(db) {
  return db.prepare('SELECT COUNT(*) AS n FROM command_log').get().n;
}
