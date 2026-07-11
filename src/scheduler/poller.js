import { getJob, updateJobPollResult } from '../db/jobs.repository.js';
import { getCredentials } from '../db/credentials.repository.js';
import { decryptCredentials } from '../crypto/credentials-crypto.js';
import { loadEnv } from '../config/env.js';
import { withUadeContext } from '../automation/browser.js';
import { runSearch } from '../automation/search.js';
import { parseResults, filterVacancies } from '../automation/parse-results.js';
import { classifySearchResult } from '../automation/classify.js';
import logger from '../logger.js';

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
      return classifySearchResult({ searchStatus: 'verified', vacancies });
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

  logger.info({ event: 'job_polled', jobId, outcome: outcome.outcome }, 'Job polled');

  return outcome;
}
