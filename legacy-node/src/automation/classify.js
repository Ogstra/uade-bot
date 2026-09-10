import { SearchOutcomeSchema } from '../schemas.js';

/**
 * Combines `runSearch`'s status (Plan 01-01) with the already parsed and
 * filtered vacancy list (Plan 01-02's `parseResults`/`filterVacancies`)
 * into the four-way `SearchOutcome` (SEARCH-04/SEARCH-05). Pure,
 * synchronous, no I/O, no Playwright import.
 *
 * The four branches are mutually exclusive and exhaustive:
 * - `searchStatus: 'invalid_credentials'` -> `{ outcome: 'invalid_credentials' }`
 * - `searchStatus: 'search_failed'` -> `{ outcome: 'search_failed', reason }`
 * - `searchStatus: 'verified'` with an empty `vacancies` array -> `{ outcome: 'no_vacancies' }`
 * - `searchStatus: 'verified'` with a non-empty `vacancies` array -> `{ outcome: 'found', vacancies }`
 *
 * The return value is always validated against `SearchOutcomeSchema`
 * before it leaves this function, so a caller can trust its shape without
 * re-checking it.
 *
 * @param {{
 *   searchStatus: 'invalid_credentials' | 'search_failed' | 'verified',
 *   reason?: string,
 *   vacancies?: import('zod').infer<typeof import('../schemas.js').VacancyRowSchema>[],
 * }} input
 * @returns {import('zod').infer<typeof SearchOutcomeSchema>}
 */
export function classifySearchResult({ searchStatus, reason, vacancies }) {
  let outcome;

  if (searchStatus === 'invalid_credentials') {
    outcome = { outcome: 'invalid_credentials' };
  } else if (searchStatus === 'search_failed') {
    outcome = { outcome: 'search_failed', reason };
  } else if (searchStatus === 'verified') {
    outcome = vacancies && vacancies.length > 0 ? { outcome: 'found', vacancies } : { outcome: 'no_vacancies' };
  } else {
    throw new Error(`classifySearchResult: unrecognized searchStatus "${searchStatus}"`);
  }

  return SearchOutcomeSchema.parse(outcome);
}
