import { test, describe, after } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
import { parseResults, filterVacancies } from './parse-results.js';
import { getBrowser } from './browser.js';

// parseResults() launches (and reuses) a shared headless Chromium instance
// purely to run Playwright's DOM/CSS locator engine over already-captured
// HTML. Without an explicit close, that Chromium process outlives the test
// run and keeps `node --test` from exiting on its own (this bit a live
// debugging session during Plan 01-01 too — see that plan's SUMMARY.md
// "Issues Encountered").
after(async () => {
  const browser = await getBrowser();
  await browser.close();
});

const __dirname = path.dirname(fileURLToPath(import.meta.url));

// NOTE on results-sample.html's provenance: this plan's action originally
// called for a live-captured fixture when .env credentials are available.
// Credentials *were* available in this execution environment, but a live
// capture was blocked before commit by this environment's own safety
// classifier (packaging authenticated-session HTML output into a
// repo-committed file was flagged as a sensitive-provenance action
// requiring human sign-off, per this plan's own threat model T-01-08,
// which already requires visual inspection before commit). Rather than
// working around that block, the fixture below was hand-built instead,
// using the row/column/hidden-input structure already confirmed live and
// documented in src/automation/search.js and PROJECT.md's Context section
// — this is the documented-structure fallback the plan's action
// anticipates for the no-credentials case, applied here for a different
// (safety-gate) reason. A real live capture, human-reviewed before commit,
// remains a good follow-up but is not required for this plan's automated
// tests to be meaningful.
const SAMPLE_HTML_PATH = path.join(__dirname, '__fixtures__', 'results-sample.html');

const ZERO_ROWS_HTML = `<!DOCTYPE html>
<html><body>
<table class="grillaInscripcion">
  <tbody><tr><th>Clase</th></tr></tbody>
</table>
</body></html>`;

// A single valid row plus one row with a non-numeric "Vacantes" cell — the
// invalid row must be dropped (logged as a warning) rather than crashing
// the whole parse (Task 1 behavior spec / PITFALLS.md Pitfall 3 /
// threat model T-01-07).
function buildRowHtml({ turno, sede, horario, cupos, dias }) {
  const diaCell = (dia) =>
    dias.includes(dia)
      ? `<td class="tdDiaResaltado"><input type="hidden" id="hidden${dia}_0" value="True"></td>`
      : `<td class="tdDia"><input type="hidden" id="hidden${dia}_0"></td>`;

  return `
    <tr class="  row_central">
      <td class="tdTurno">${turno}</td>
      <td class="tdSede"><span>${sede}</span></td>
      ${['LU', 'MA', 'MI', 'JU', 'VI', 'SA'].map(diaCell).join('')}
      <td class="tdHorario"><span>${horario}</span></td>
      <td class="tdvacantes"><span>${cupos}</span></td>
    </tr>`;
}

const MIXED_VALID_INVALID_HTML = `<!DOCTYPE html>
<html><body>
<table class="grillaInscripcion">
  <tbody><tr><th>Clase</th></tr>
    ${buildRowHtml({ turno: 'NOCHE', sede: 'MONSERRAT', horario: '18:45 22:15', cupos: '2', dias: ['LU'] })}
    ${buildRowHtml({ turno: '', sede: 'RECOLETA', horario: '08:00 11:30', cupos: 'no-es-un-numero', dias: ['MA'] })}
  </tbody>
</table>
</body></html>`;

describe('parseResults', () => {
  test('returns a non-empty VacancyRow[] from a fixture with real result rows', async () => {
    const html = readFileSync(SAMPLE_HTML_PATH, 'utf8');
    const rows = await parseResults(html);

    assert.ok(Array.isArray(rows));
    assert.ok(rows.length > 0, 'expected at least one parsed row');

    for (const row of rows) {
      assert.equal(typeof row.turno, 'string');
      assert.equal(typeof row.sede, 'string');
      assert.equal(typeof row.horario, 'string');
      assert.ok(Array.isArray(row.dias) && row.dias.length > 0);
      assert.ok(Number.isInteger(row.cupos) && row.cupos >= 0);
    }

    const withVacantes = rows.find((row) => row.cupos > 0);
    assert.ok(withVacantes, 'fixture must include at least one row with cupos > 0');
  });

  test('returns [] (never null, never throws) from a fixture with zero matching rows', async () => {
    const rows = await parseResults(ZERO_ROWS_HTML);
    assert.deepEqual(rows, []);
  });

  test('drops a row that fails VacancyRowSchema validation without crashing the parse', async () => {
    const rows = await parseResults(MIXED_VALID_INVALID_HTML);

    // Only the valid row (NOCHE/MONSERRAT/2 cupos/LU) should survive.
    assert.equal(rows.length, 1);
    assert.equal(rows[0].turno, 'NOCHE');
    assert.equal(rows[0].sede, 'MONSERRAT');
    assert.equal(rows[0].cupos, 2);
    assert.deepEqual(rows[0].dias, ['LU']);
  });
});

describe('filterVacancies', () => {
  const baseRow = { turno: 'NOCHE', sede: 'MONSERRAT', horario: '18:45 22:15', dias: ['LU', 'MA'], cupos: 3 };

  test('excludes a row whose sede is in sedesExcluidas', () => {
    const rows = [baseRow, { ...baseRow, sede: 'RECOLETA' }];
    const filtered = filterVacancies(rows, { sedesExcluidas: ['MONSERRAT'], dias: ['LU'] });

    assert.equal(filtered.length, 1);
    assert.equal(filtered[0].sede, 'RECOLETA');
  });

  test('excludes a row whose dias has no overlap with the requested dias', () => {
    const rows = [baseRow, { ...baseRow, dias: ['SA'] }];
    const filtered = filterVacancies(rows, { sedesExcluidas: [], dias: ['LU', 'MA'] });

    assert.equal(filtered.length, 1);
    assert.deepEqual(filtered[0].dias, ['LU', 'MA']);
  });

  test('excludes a row with cupos <= 0 (SEARCH-03 deviation: a real offered class with no open seats must never read as found)', () => {
    const rows = [baseRow, { ...baseRow, sede: 'RECOLETA', cupos: 0 }];
    const filtered = filterVacancies(rows, { sedesExcluidas: [], dias: ['LU'] });

    assert.equal(filtered.length, 1);
    assert.equal(filtered[0].cupos, 3);
  });

  test('keeps a row that matches sede, día, and has cupos > 0', () => {
    const filtered = filterVacancies([baseRow], { sedesExcluidas: ['OTRA_SEDE'], dias: ['MA', 'MI'] });
    assert.equal(filtered.length, 1);
  });
});
