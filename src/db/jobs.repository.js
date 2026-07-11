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
    status: row.status,
    lastPolledAt: row.last_polled_at,
    lastOutcome: row.last_outcome,
    createdAt: row.created_at,
  });
}

/**
 * Inserts a new `jobs` row with `status = 'active'`. `filtros` is validated
 * against `FiltrosSchema` and `JSON.stringify`d into `filtros_json`.
 *
 * @param {import('better-sqlite3').Database} db
 * @param {{ discordUserId: string, filtros: import('zod').infer<typeof FiltrosSchema> }} params
 * @returns {import('zod').infer<typeof SearchJobSchema>}
 */
export function createJob(db, { discordUserId, filtros }) {
  const now = Date.now();
  const parsedFiltros = FiltrosSchema.parse(filtros);

  const info = db
    .prepare(
      `INSERT INTO jobs (discord_user_id, filtros_json, status, created_at)
       VALUES (?, ?, 'active', ?)`,
    )
    .run(discordUserId, JSON.stringify(parsedFiltros), now);

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
