import { parseArgs } from 'node:util';
import { FiltrosSchema } from './schemas.js';
import { loadEnv } from './config/env.js';
import { getBrowser, withUadeContext } from './automation/browser.js';
import { runSearch } from './automation/search.js';
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
 * Runs one complete search: env -> browser -> search -> parse -> classify.
 * Prints the resulting `SearchOutcome` as formatted JSON to stdout — never
 * the credentials object, never the raw postback HTML (threat model
 * T-01-06). Every diagnostic/error message is routed through `logger`
 * (which redacts credential-shaped fields) rather than a direct console
 * call.
 */
async function main() {
  const filtros = parseFiltrosFromArgv(process.argv.slice(2));
  const { UADE_USERNAME, UADE_PASSWORD } = loadEnv();

  const outcome = await withUadeContext({ username: UADE_USERNAME, password: UADE_PASSWORD }, async (context) => {
    const result = await runSearch(context, filtros);

    let vacancies = [];
    if (result.status === 'verified') {
      const rows = await parseResults(result.html);
      vacancies = filterVacancies(rows, filtros);
    }

    return classifySearchResult({ searchStatus: result.status, reason: result.reason, vacancies });
  });

  process.stdout.write(`${JSON.stringify(outcome, null, 2)}\n`);
  return outcome;
}

main()
  .then((outcome) => {
    process.exitCode = outcome.outcome === 'invalid_credentials' || outcome.outcome === 'search_failed' ? 1 : 0;
  })
  .catch((err) => {
    logger.error({ event: 'cli_failed', message: err.message }, 'CLI run failed');
    process.exitCode = 1;
  })
  .finally(async () => {
    // getBrowser() is a shared singleton kept alive across this whole
    // process — withUadeContext only closes its BrowserContext, not the
    // underlying Browser connection. Without this, the process hangs
    // indefinitely after printing its result (or an error), even with
    // process.exitCode set, because the event loop never empties. Bit both
    // a standalone diagnostic script and a test file during this phase's
    // live debugging; fixed here for the actual shipped CLI entry point.
    await (await getBrowser()).close();
  });
