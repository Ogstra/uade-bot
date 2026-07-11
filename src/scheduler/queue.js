import PQueue from 'p-queue';
import { listActiveJobs } from '../db/jobs.repository.js';
import { getUser } from '../db/users.repository.js';
import { loadEnv } from '../config/env.js';
import { pollOnce } from './poller.js';
import logger from '../logger.js';

/**
 * Groups an array of job objects by `discordUserId`. Pure, no I/O.
 *
 * @param {{ discordUserId: string }[]} jobs
 * @returns {Map<string, object[]>}
 */
export function groupActiveJobsByAccount(jobs) {
  const jobsByAccount = new Map();

  for (const job of jobs) {
    if (!jobsByAccount.has(job.discordUserId)) {
      jobsByAccount.set(job.discordUserId, []);
    }
    jobsByAccount.get(job.discordUserId).push(job);
  }

  return jobsByAccount;
}

/**
 * Selects exactly one job per account for this tick, round-robin across
 * each account's own job list (SCHED-02, D-03) — an account with several
 * active jobs still only surfaces one per tick, regardless of how many
 * other accounts/jobs exist. Pure, no I/O.
 *
 * `pointers` is a raw, ever-incrementing per-account counter (missing keys
 * default to `0`); wraparound is handled by the NEXT call's modulo
 * indexing, not this one, so callers never need to reset a pointer.
 *
 * @param {Map<string, object[]>} jobsByAccount
 * @param {Map<string, number>} pointers
 * @returns {{ selected: object[], nextPointers: Map<string, number> }}
 */
export function selectRoundRobinTick(jobsByAccount, pointers) {
  const selected = [];
  const nextPointers = new Map(pointers);

  for (const [accountId, jobs] of jobsByAccount) {
    if (jobs.length === 0) {
      continue;
    }

    const pointer = pointers.get(accountId) ?? 0;
    const index = pointer % jobs.length;

    selected.push(jobs[index]);
    nextPointers.set(accountId, pointer + 1);
  }

  return { selected, nextPointers };
}

/**
 * A job's account is excluded from tick selection when it currently has a
 * non-null `pauseReason` AND either `pauseUntil` is `null` (indefinite
 * pause, D-05/D-07) or `pauseUntil` is still in the future (active backoff
 * window, D-06). An account whose backoff window has already elapsed
 * (`pauseUntil <= Date.now()`) is NOT excluded here — clearing a stale
 * pause once its window elapses is Plan 02-03's scheduler-layer concern,
 * not this filter's.
 *
 * @param {import('better-sqlite3').Database} db
 * @param {string} discordUserId
 * @returns {boolean}
 */
function isAccountPaused(db, discordUserId) {
  const user = getUser(db, discordUserId);

  if (!user || !user.pauseReason) {
    return false;
  }

  if (user.pauseUntil === null) {
    return true;
  }

  return user.pauseUntil > Date.now();
}

/**
 * Coordinates polling across every active, non-paused job: a p-queue-backed
 * global concurrency cap (SCHED-01, D-08) bounds how many `pollOnceFn` calls
 * ever run at once, and round-robin per-account selection (SCHED-02, D-03)
 * ensures an account with several active searches still generates only
 * ~1 request per polling interval, not one request per search.
 *
 * @param {{
 *   db: import('better-sqlite3').Database,
 *   intervalMs?: number,
 *   concurrency?: number,
 *   pollOnceFn?: typeof pollOnce,
 * }} params
 * @returns {{ start: () => void, stop: () => void }}
 */
export function createScheduler({
  db,
  intervalMs = loadEnv().POLL_INTERVAL_MS,
  concurrency = loadEnv().SCHEDULER_CONCURRENCY,
  pollOnceFn = pollOnce,
} = {}) {
  const pQueue = new PQueue({ concurrency });
  let pointers = new Map();
  let intervalHandle;
  const registeredJobIds = new Set();
  // Tracks accounts (discordUserId) with a pollOnceFn call currently
  // in-flight, so an overlapping tick (e.g. a slow poll that outlives
  // intervalMs) never queues a second concurrent poll for the same
  // account — pollOnce's read-then-write of account-level pause/backoff
  // state (src/scheduler/poller.js) is not safe to run concurrently for
  // the same discordUserId.
  const inFlightAccountIds = new Set();

  function tick() {
    const activeJobs = listActiveJobs(db).filter((job) => !isAccountPaused(db, job.discordUserId));
    const jobsByAccount = groupActiveJobsByAccount(activeJobs);
    const { selected, nextPointers } = selectRoundRobinTick(jobsByAccount, pointers);
    pointers = nextPointers;

    logger.info({ event: 'scheduler_tick', selectedCount: selected.length }, 'Scheduler tick');

    for (const job of selected) {
      if (inFlightAccountIds.has(job.discordUserId)) {
        continue;
      }
      inFlightAccountIds.add(job.discordUserId);
      pQueue
        .add(() => pollOnceFn(db, job.id))
        .catch((err) => {
          logger.error(
            { event: 'poll_job_failed', jobId: job.id, message: err.message },
            'pollOnce failed for job',
          );
        })
        .finally(() => {
          inFlightAccountIds.delete(job.discordUserId);
        });
    }
  }

  return {
    start() {
      intervalHandle = setInterval(tick, intervalMs);
    },
    stop() {
      clearInterval(intervalHandle);
    },
    /**
     * Marks `job.id` as reconstructed/eligible for this scheduler instance
     * (SCHED-04). A no-op, not an error, if `job.id` is already registered
     * — this is what makes `src/scheduler/bootstrap.js`'s restart
     * reconstruction idempotent across repeated restarts. The tick loop's
     * own `listActiveJobs(db)` call remains the source of truth for WHICH
     * jobs are currently active each tick; this `Set` only answers "has
     * this job id already been reconstructed."
     *
     * @param {{ id: number }} job
     */
    registerJob(job) {
      registeredJobIds.add(job.id);
    },
    /**
     * @param {number} jobId
     * @returns {boolean}
     */
    isRegistered(jobId) {
      return registeredJobIds.has(jobId);
    },
  };
}
