import { listActiveJobs } from '../db/jobs.repository.js';
import logger from '../logger.js';

/**
 * Reconstructs every previously-active job into a running scheduler at
 * process startup (SCHED-04) — the literal "restarting the process never
 * duplicates polling work" guarantee. Reads every `status = 'active'` job
 * via `listActiveJobs` and registers each with `scheduler` only if it isn't
 * already registered (`scheduler.isRegistered(job.id)`), which is what
 * makes calling this function twice against the same database/scheduler
 * (simulating two restarts) never register the same job twice — even
 * though `scheduler.registerJob` itself is already `Set`-safe to call
 * twice, this explicit guard makes the idempotency intent visible here.
 *
 * @param {import('better-sqlite3').Database} db
 * @param {{ registerJob: (job: object) => void, isRegistered: (jobId: number) => boolean }} scheduler
 * @returns {Promise<void>}
 */
export async function reconstructActiveJobs(db, scheduler) {
  const activeJobs = listActiveJobs(db);

  for (const job of activeJobs) {
    if (!scheduler.isRegistered(job.id)) {
      scheduler.registerJob(job);
    }
  }

  logger.info({ event: 'jobs_reconstructed', count: activeJobs.length }, 'Active jobs reconstructed');
}
