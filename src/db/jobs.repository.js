import { SearchJobSchema, FiltrosSchema } from '../schemas.js';
import logger from '../logger.js';

/**
 * @param {object | undefined} row
 * @returns {import('zod').infer<typeof SearchJobSchema> | null}
 */
function rowToJob(row) {
  if (!row) {
    return null;
  }

  const filtros = FiltrosSchema.parse(JSON.parse(row.filtros_json));

  return SearchJobSchema.parse({
    id: row.id,
    discordUserId: row.discord_user_id,
    filtros,
    channelId: row.channel_id ?? null,
    label: row.label || filtros.materiaCodigo,
    status: row.status,
    lastPolledAt: row.last_polled_at,
    lastOutcome: row.last_outcome,
    lastNotifiedState: row.last_notified_state,
    lastNotifiedCupos: row.last_notified_cupos,
    createdAt: row.created_at,
  });
}

/**
 * Inserts a new `jobs` row with `status = 'active'`. `filtros` is validated
 * against `FiltrosSchema` and `JSON.stringify`d into `filtros_json`.
 *
 * @param {import('better-sqlite3').Database} db
 * @param {{ discordUserId: string, filtros: import('zod').infer<typeof FiltrosSchema>, channelId?: string | null, label?: string }} params
 * @returns {import('zod').infer<typeof SearchJobSchema>}
 */
export function createJob(db, { discordUserId, filtros, channelId = null, label }) {
  const now = Date.now();
  const parsedFiltros = FiltrosSchema.parse(filtros);
  const parsedLabel = label || parsedFiltros.materiaCodigo;

  const info = db
    .prepare(
      `INSERT INTO jobs (discord_user_id, filtros_json, channel_id, label, status, created_at)
       VALUES (?, ?, ?, ?, 'active', ?)`,
    )
    .run(discordUserId, JSON.stringify(parsedFiltros), channelId, parsedLabel, now);

  logger.info(
    { event: 'job_created', discordUserId, jobId: Number(info.lastInsertRowid) },
    'Search job created',
  );

  return getJob(db, Number(info.lastInsertRowid));
}

/**
 * @param {import('better-sqlite3').Database} db
 * @param {number} jobId
 * @returns {import('zod').infer<typeof SearchJobSchema> | null}
 */
export function getJob(db, jobId) {
  const row = db.prepare('SELECT * FROM jobs WHERE id = ?').get(jobId);
  return rowToJob(row);
}

/**
 * @param {import('better-sqlite3').Database} db
 * @returns {import('zod').infer<typeof SearchJobSchema>[]}
 */
export function listActiveJobs(db) {
  const rows = db.prepare("SELECT * FROM jobs WHERE status = 'active'").all();
  return rows.map(rowToJob);
}

/**
 * Every job across every account, active or paused -- admin-only
 * visibility (unlike `listJobsByUser`, not scoped to a single caller).
 *
 * @param {import('better-sqlite3').Database} db
 * @returns {import('zod').infer<typeof SearchJobSchema>[]}
 */
export function listAllJobs(db) {
  const rows = db.prepare('SELECT * FROM jobs ORDER BY id ASC').all();
  return rows.map(rowToJob);
}

/**
 * @param {import('better-sqlite3').Database} db
 * @param {string} discordUserId
 * @returns {import('zod').infer<typeof SearchJobSchema>[]}
 */
export function listJobsByUser(db, discordUserId) {
  const rows = db
    .prepare(
      `SELECT * FROM jobs
       WHERE discord_user_id = ?
         AND status IN ('active', 'paused_by_user')
       ORDER BY id ASC`,
    )
    .all(discordUserId);
  return rows.map(rowToJob);
}

/**
 * @param {import('better-sqlite3').Database} db
 * @param {number} jobId
 * @param {'active' | 'paused_by_user'} status
 * @returns {import('zod').infer<typeof SearchJobSchema>}
 */
export function updateJobStatus(db, jobId, status) {
  SearchJobSchema.shape.status.parse(status);

  db.prepare('UPDATE jobs SET status = ? WHERE id = ?').run(status, jobId);

  logger.info({ event: 'job_status_update', jobId, status }, 'Job status updated');

  return getJob(db, jobId);
}

/**
 * @param {import('better-sqlite3').Database} db
 * @param {number} jobId
 * @returns {boolean}
 */
export function deleteJob(db, jobId) {
  const info = db.prepare('DELETE FROM jobs WHERE id = ?').run(jobId);

  logger.info({ event: 'job_deleted', jobId, deleted: info.changes > 0 }, 'Job deleted');

  return info.changes > 0;
}

/**
 * @param {import('better-sqlite3').Database} db
 * @param {number} jobId
 * @param {{ lastPolledAt: number, lastOutcome: string }} result
 * @returns {import('zod').infer<typeof SearchJobSchema>}
 */
export function updateJobPollResult(db, jobId, { lastPolledAt, lastOutcome }) {
  db.prepare('UPDATE jobs SET last_polled_at = ?, last_outcome = ? WHERE id = ?').run(
    lastPolledAt,
    lastOutcome,
    jobId,
  );

  logger.info({ event: 'job_poll_result_update', jobId, lastOutcome }, 'Job poll result updated');

  return getJob(db, jobId);
}

/**
 * @param {import('better-sqlite3').Database} db
 * @param {number} jobId
 * @param {{ lastNotifiedState: string | null, lastNotifiedCupos: number | null }} state
 * @returns {import('zod').infer<typeof SearchJobSchema>}
 */
export function updateJobNotifiedState(db, jobId, { lastNotifiedState, lastNotifiedCupos }) {
  db.prepare('UPDATE jobs SET last_notified_state = ?, last_notified_cupos = ? WHERE id = ?').run(
    lastNotifiedState,
    lastNotifiedCupos,
    jobId,
  );

  logger.info({ event: 'job_notified_state_update', jobId }, 'Job notification state updated');

  return getJob(db, jobId);
}
