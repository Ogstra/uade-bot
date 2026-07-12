import { getJob, updateJobPollResult } from '../db/jobs.repository.js';
import { getCredentials } from '../db/credentials.repository.js';
import { getUser, updateAccountPauseState } from '../db/users.repository.js';
import { upsertMateriaNombre } from '../db/materias.repository.js';
import { decryptCredentials } from '../crypto/credentials-crypto.js';
import { loadEnv } from '../config/env.js';
import { withUadeContext } from '../automation/browser.js';
import { runSearch } from '../automation/search.js';
import { parseResults, filterVacancies } from '../automation/parse-results.js';
import { classifySearchResult } from '../automation/classify.js';
import { nextBackoffState } from './backoff.js';
import logger from '../logger.js';

/**
 * Maps a `result.status` from `runSearch` (Plan 02-03) onto the backoff
 * signal `nextBackoffState` consumes. `search_failed` intentionally maps to
 * `'success'`, not a pause signal: D-04/D-05/D-06's scope is specifically
 * credential/rate-limit/stale-URL signals — a transient `search_failed`
 * like `postback_mismatch` must NOT indefinitely pause an account. Mapping
 * it to `'success'` simply clears any stale pause left over from a prior
 * transient issue; it is not itself a stronger claim that the account is
 * healthy.
 *
 * @param {string} status
 * @returns {'invalid_credentials' | 'rate_limited' | 'stale_start_url' | 'success'}
 */
function backoffSignalFromStatus(status) {
  if (status === 'invalid_credentials' || status === 'rate_limited' || status === 'stale_start_url') {
    return status;
  }
  return 'success';
}

/**
 * Maps an `AccountPauseStateSchema`-shaped row (Phase 2, D-04–D-07) back to
 * the `{ pauseReason, pauseUntil, backoffAttempt }` shape
 * `updateAccountPauseState` persists onto the `users` table.
 *
 * @param {import('zod').infer<typeof import('../schemas.js').AccountPauseStateSchema>} state
 * @returns {{ pauseReason: string | null, pauseUntil: number | null, backoffAttempt: number }}
 */
function pauseStateToUserFields(state) {
  if (state.reason === 'none') {
    return { pauseReason: null, pauseUntil: null, backoffAttempt: 0 };
  }
  if (state.reason === 'rate_limited') {
    return { pauseReason: 'rate_limited', pauseUntil: state.resumeAt, backoffAttempt: state.backoffAttempt };
  }
  return { pauseReason: state.reason, pauseUntil: null, backoffAttempt: 0 };
}

/**
 * Maps a `users` row (as returned by `getUser`) to the
 * `AccountPauseStateSchema`-shaped object `nextBackoffState` expects as its
 * `currentState` — `pauseReason === null` maps to `{ reason: 'none' }` (a
 * fresh/never-paused account).
 *
 * @param {import('zod').infer<typeof import('../schemas.js').UserRecordSchema> | null} user
 * @returns {import('zod').infer<typeof import('../schemas.js').AccountPauseStateSchema>}
 */
function userToCurrentPauseState(user) {
  if (!user || !user.pauseReason) {
    return { reason: 'none' };
  }
  if (user.pauseReason === 'rate_limited') {
    return { reason: 'rate_limited', backoffAttempt: user.backoffAttempt, resumeAt: user.pauseUntil };
  }
  return { reason: user.pauseReason };
}

/**
 * Runs one complete poll for a single search job: decrypts that job's
 * account credentials transiently, drives the real Phase-1 search chain
 * (`withUadeContext` -> `runSearch` -> `parseResults` -> `filterVacancies` ->
 * `classifySearchResult`) with the job's own decrypted `uadeStartUrl`, and
 * persists the resulting outcome onto the job's row.
 *
 * CRED-04/CRED-05 discipline: `decryptCredentials`'s output is destructured
 * into a single local `const` used only inside this function body, passed
 * directly into `withUadeContextFn`/`runSearchFn`, never assigned to any
 * object that outlives this call, and never logged — only the final
 * `outcome.outcome` string is logged.
 *
 * @param {import('better-sqlite3').Database} db
 * @param {number} jobId
 * @param {{
 *   withUadeContextFn?: typeof withUadeContext,
 *   runSearchFn?: typeof runSearch,
 * }} [deps]
 * @returns {Promise<import('zod').infer<typeof import('../schemas.js').SearchOutcomeSchema> | { outcome: string }>}
 */
export async function pollOnce(db, jobId, { withUadeContextFn = withUadeContext, runSearchFn = runSearch } = {}) {
  const job = getJob(db, jobId);
  if (!job) {
    throw new Error(`pollOnce: job ${jobId} not found`);
  }

  const credentialsRow = getCredentials(db, job.discordUserId);
  if (!credentialsRow) {
    throw new Error(`pollOnce: no credentials found for discordUserId ${job.discordUserId}`);
  }

  const { CREDENTIALS_MASTER_KEY } = loadEnv();
  const { uadeUsername, uadePassword, uadeStartUrl } = decryptCredentials(
    CREDENTIALS_MASTER_KEY,
    job.discordUserId,
    credentialsRow,
  );

  const outcome = await withUadeContextFn({ username: uadeUsername, password: uadePassword }, async (context) => {
    const result = await runSearchFn(context, job.filtros, { startUrl: uadeStartUrl });

    if (result.status === 'verified') {
      const rows = await parseResults(result.html);
      const vacancies = filterVacancies(rows, job.filtros);
      const outcome = classifySearchResult({ searchStatus: 'verified', vacancies });
      return result.materiaNombre ? { ...outcome, materiaNombre: result.materiaNombre } : outcome;
    }

    if (result.status === 'search_failed') {
      return classifySearchResult({ searchStatus: 'search_failed', reason: result.reason });
    }

    if (result.status === 'invalid_credentials') {
      return classifySearchResult({ searchStatus: 'invalid_credentials' });
    }

    // Statuses introduced by Plan 02-03 ('rate_limited' / 'stale_start_url')
    // — not yet returned by runSearchFn in this plan, but this branch must
    // exist so pollOnce doesn't throw once Plan 02-03 lands. Intentionally
    // NOT passed to classifySearchResult/SearchOutcomeSchema, which don't
    // recognize these — this is a scheduler-internal status, not a
    // SearchOutcome.
    return { outcome: result.status };
  });

  updateJobPollResult(db, jobId, { lastPolledAt: Date.now(), lastOutcome: JSON.stringify(outcome) });

  if (outcome.materiaNombre) {
    upsertMateriaNombre(db, job.filtros.materiaCodigo, outcome.materiaNombre);
  }

  // D-04: this single write pauses/resumes EVERY job tied to this account,
  // since queue.js's tick filter reads this same account-level state for
  // every job at this discordUserId on the next tick — not a per-job write.
  const currentPauseState = userToCurrentPauseState(getUser(db, job.discordUserId));
  const signal = backoffSignalFromStatus(outcome.outcome);
  const nextPauseState = nextBackoffState({ currentState: currentPauseState, signal });
  updateAccountPauseState(db, job.discordUserId, pauseStateToUserFields(nextPauseState));

  logger.info({ event: 'job_polled', jobId, outcome: outcome.outcome }, 'Job polled');

  return outcome;
}
