import { VacancyRowSchema } from '../schemas.js';
import { load } from 'cheerio/slim';
import logger from '../logger.js';

// Results rows use one of three classes depending on how the site tags that
// particular offering — confirmed live 2026-07-11 (see search.js and
// PROJECT.md's Context section). `row_recoleta` was not observed in the one
// live capture taken so far, but is documented and included defensively.
const ROW_SELECTOR = 'tr.row_central, tr.row_recoleta, tr.rowTagueadoNuevo';

// Each row's día columns carry hidden inputs whose `id` CONTAINS (not
// equals) one of these substrings, suffixed with a per-row index
// (e.g. `..._hiddenJU_0`). A hidden input is "true" for that día only when
// it has `value="True"` — days the class doesn't meet have no `value`
// attribute at all (confirmed live 2026-07-11).
const DIA_HIDDEN_INPUT_SUBSTRINGS = {
  LU: 'hiddenLU',
  MA: 'hiddenMA',
  MI: 'hiddenMI',
  JU: 'hiddenJU',
  VI: 'hiddenVI',
  SA: 'hiddenSA',
};

/**
 * Extracts every día for which this row's hidden input carries
 * `value="True"`.
 *
 * @param {import('cheerio').Cheerio<import('domhandler').Element>} row
 * @returns {string[]}
 */
function extractDias(row) {
  const dias = [];
  for (const [dia, idSubstring] of Object.entries(DIA_HIDDEN_INPUT_SUBSTRINGS)) {
    const value = row.find(`input[id*="${idSubstring}"]`).first().attr('value');
    if (value === 'True') {
      dias.push(dia);
    }
  }
  return dias;
}

/**
 * Extracts a single results row into a raw candidate object (pre-schema-
 * validation) — turno/sede/horario/cupos cell text plus the row's día set.
 *
 * @param {import('cheerio').Cheerio<import('domhandler').Element>} row
 * @returns {{ turno: string, sede: string, horario: string, dias: string[], cupos: number|string }}
 */
function extractRow(row) {
  const turnoText = row.find('td.tdTurno').first().text();
  const sedeText = row.find('td.tdSede').first().text();
  const horarioText = row.find('td.tdHorario').first().text();
  const cuposText = row.find('td.tdvacantes').first().text();

  const cuposTrimmed = cuposText.trim();
  const cupos = /^\d+$/.test(cuposTrimmed) ? Number.parseInt(cuposTrimmed, 10) : cuposTrimmed;

  return {
    turno: turnoText.trim(),
    sede: sedeText.trim(),
    horario: horarioText.trim(),
    dias: extractDias(row),
    cupos,
  };
}

/**
 * Parses a captured HTML snapshot of the UADE results page into a validated
 * `VacancyRow[]` using Cheerio's inert, browserless DOM implementation.
 *
 * A row whose extracted fields fail `VacancyRowSchema` validation is
 * dropped (logged as a warning) rather than crashing the whole parse — a
 * single malformed/unexpected row must never take down an otherwise-good
 * result set (PITFALLS.md Pitfall 3 / this plan's threat model T-01-07).
 *
 * @param {string} html
 * @returns {Promise<import('zod').infer<typeof VacancyRowSchema>[]>}
 */
export async function parseResults(html) {
  const $ = load(html);
  const vacancies = [];

  for (const element of $(ROW_SELECTOR).toArray()) {
    const candidate = extractRow($(element));
    const result = VacancyRowSchema.safeParse(candidate);

    if (result.success) {
      vacancies.push(result.data);
    } else {
      const issues = result.error.issues.map(({ code, path }) => ({ code, path }));
      logger.warn(
        { event: 'vacancy_row_invalid', issues },
        'Dropping a results row that failed VacancyRowSchema validation'
      );
    }
  }

  return vacancies;
}

/**
 * Filters a parsed `VacancyRow[]` down to the rows that are actually
 * relevant to the caller's search (SEARCH-03), applying three rules:
 *
 * - Exclude any row whose `sede` is in `sedesExcluidas`.
 * - Exclude any row whose `dias` has no overlap with the requested `dias`.
 * - Exclude any row with `cupos <= 0` — the results table lists every
 *   offered class regardless of whether it currently has open seats, so
 *   without this rule a real class with zero vacantes would incorrectly
 *   flow through to a `found` outcome (violates this plan's must-have: "a
 *   filter combination known to have zero open seats prints a
 *   'no_vacancies' result — never a crash, never a false 'found'").
 *
 * Pure, synchronous, no I/O, no Playwright dependency.
 *
 * @param {import('zod').infer<typeof VacancyRowSchema>[]} rows
 * @param {{ sedesExcluidas?: string[], dias: string[] }} filtros
 * @returns {import('zod').infer<typeof VacancyRowSchema>[]}
 */
export function filterVacancies(rows, { sedesExcluidas = [], dias }) {
  return rows.filter((row) => {
    if (sedesExcluidas.includes(row.sede)) {
      return false;
    }
    const hasOverlap = row.dias.some((dia) => dias.includes(dia));
    if (!hasOverlap) {
      return false;
    }
    if (row.cupos <= 0) {
      return false;
    }
    return true;
  });
}
