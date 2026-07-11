import { AccountPauseStateSchema } from '../schemas.js';

/**
 * D-06's exact exponential backoff sequence, in milliseconds: 1, 2, 5, and
 * 15 minutes. A signal beyond the 4th consecutive `rate_limited` reuses the
 * last (largest) entry rather than growing unbounded or indexing out of
 * `nextBackoffState`'s bounds.
 */
export const BACKOFF_SEQUENCE_MS = [60000, 120000, 300000, 900000];

/**
 * Pure, synchronous account-level pause/backoff state machine implementing
 * D-04 through D-07. No I/O, no DB/Playwright import — mirrors
 * `src/automation/classify.js`'s shape: input object in,
 * `AccountPauseStateSchema.parse(...)`-validated object out.
 *
 * - `signal: 'success'` always clears any prior pause (`{ reason: 'none' }`),
 *   resetting the backoff counter.
 * - `signal: 'invalid_credentials'` / `'stale_start_url'` always overrides
 *   `currentState` with the corresponding indefinite pause reason — the most
 *   recent poll result is authoritative, regardless of what was paused
 *   before.
 * - `signal: 'rate_limited'` increments the backoff attempt counter (reset
 *   to 0 unless `currentState` is itself a `rate_limited` state, i.e. this
 *   is a consecutive rate-limit signal) and computes `resumeAt` from
 *   `BACKOFF_SEQUENCE_MS`, capping the index at the array's last entry
 *   rather than growing unbounded.
 *
 * @param {{
 *   currentState: import('zod').infer<typeof AccountPauseStateSchema>,
 *   signal: 'invalid_credentials' | 'rate_limited' | 'stale_start_url' | 'success',
 * }} params
 * @returns {import('zod').infer<typeof AccountPauseStateSchema>}
 */
export function nextBackoffState({ currentState, signal }) {
  let nextState;

  if (signal === 'success') {
    nextState = { reason: 'none' };
  } else if (signal === 'invalid_credentials') {
    nextState = { reason: 'needs_credentials' };
  } else if (signal === 'stale_start_url') {
    nextState = { reason: 'needs_new_start_url' };
  } else if (signal === 'rate_limited') {
    const priorAttempt = currentState?.reason === 'rate_limited' ? currentState.backoffAttempt : 0;
    const backoffAttempt = priorAttempt + 1;
    const delayIndex = Math.min(backoffAttempt - 1, BACKOFF_SEQUENCE_MS.length - 1);
    const resumeAt = Date.now() + BACKOFF_SEQUENCE_MS[delayIndex];
    nextState = { reason: 'rate_limited', backoffAttempt, resumeAt };
  } else {
    throw new Error(`nextBackoffState: unrecognized signal "${signal}"`);
  }

  return AccountPauseStateSchema.parse(nextState);
}
