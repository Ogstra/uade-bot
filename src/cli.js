import { parseArgs } from 'node:util';
import { pathToFileURL } from 'node:url';
import { FiltrosSchema } from './schemas.js';
import { loadEnv } from './config/env.js';
import { withHttpSession } from './automation/http-session.js';
import { runHttpSearch } from './automation/http-search.js';
import { parseResults, filterVacancies } from './automation/parse-results.js';
import { classifySearchResult } from './automation/classify.js';
import logger from './logger.js';

/**
 * Parses `--materia`, `--ofrecimiento`, `--turno`, `--dias` (comma-
 * separated), and `--sedes-excluidas` (comma-separated, optional) from
 * `process.argv` into a `filtros` object validated against `FiltrosSchema`.
 *
 * @param {string[]} argv
 * @returns {import('zod').infer<typeof FiltrosSchema>}
 */
function parseFiltrosFromArgv(argv) {
  const { values } = parseArgs({
    args: argv,
    options: {
      materia: { type: 'string' },
      ofrecimiento: { type: 'string' },
      turno: { type: 'string' },
      dias: { type: 'string' },
      'sedes-excluidas': { type: 'string' },
    },
  });

  const filtros = {
    materiaCodigo: values.materia,
    ofrecimiento: values.ofrecimiento,
    turno: values.turno,
    dias: values.dias ? values.dias.split(',').filter(Boolean) : [],
    sedesExcluidas: values['sedes-excluidas'] ? values['sedes-excluidas'].split(',').filter(Boolean) : [],
  };

  return FiltrosSchema.parse(filtros);
}

/**
 * Runs one complete search: env -> scoped HTTP session -> search -> parse -> classify.
 * Prints the resulting `SearchOutcome` as formatted JSON to stdout — never
 * the credentials object, never the raw postback HTML (threat model
 * T-01-06). Every diagnostic/error message is routed through `logger`
 * (which redacts credential-shaped fields) rather than a direct console
 * call.
 */
async function main() {
  const filtros = parseFiltrosFromArgv(process.argv.slice(2));
  const { UADE_USERNAME, UADE_PASSWORD, UADE_START_URL } = loadEnv();

  const outcome = await withHttpSession({ username: UADE_USERNAME, password: UADE_PASSWORD }, async (session) => {
    const result = await runHttpSearch(session, filtros, { startUrl: UADE_START_URL });

    let vacancies = [];
    if (result.status === 'verified') {
      const parsedResults = await parseResults(result.html);
      if (parsedResults.invalidRowCount > 0 || !parsedResults.resultsContainerDetected) {
        return classifySearchResult({ searchStatus: 'search_failed', reason: 'result_parse_failed', vacancies });
      }
      vacancies = filterVacancies(parsedResults.rows, filtros);
    }

    const searchStatus = result.status === 'rate_limited' || result.status === 'stale_start_url'
      ? 'search_failed'
      : result.status;
    const reason = result.reason ?? (searchStatus === 'search_failed' ? result.status : undefined);
    return classifySearchResult({ searchStatus, reason, vacancies });
  });

  process.stdout.write(`${JSON.stringify(outcome, null, 2)}\n`);
  return outcome;
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  main()
    .then((outcome) => {
      process.exitCode = outcome.outcome === 'invalid_credentials' || outcome.outcome === 'search_failed' ? 1 : 0;
    })
    .catch((err) => {
      logger.error({ event: 'cli_failed', message: err.message }, 'CLI run failed');
      process.exitCode = 1;
    });
}
