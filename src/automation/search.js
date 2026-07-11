import { FiltrosSchema } from '../schemas.js';
import { loadEnv } from '../config/env.js';
import logger from '../logger.js';

export const SEARCH_URL = 'https://inscripcionespia.uade.edu.ar/InscripcionClaseBuscar.aspx';

// KNOWN LIMITATION (confirmed live 2026-07-11): navigating straight to
// SEARCH_URL authenticates via Basic Auth, but the ASP.NET session never gets
// the Periodo/Carrera/Turno combos populated — the portal's own signed entry
// link (UADE_START_URL, developer-local env var) is what actually establishes
// that session state. Navigation below goes through UADE_START_URL first;
// SEARCH_URL is kept only as a pathname reference for matching the postback
// response, since the resolved page URL after the portal redirect may carry
// a different query string than the bare constant.

// --- Selectors ---------------------------------------------------------
// Sourced from PROJECT.md's live investigation of the UADE site
// (2026-07-10): ofrecimiento is a radio group (curricular=145,
// optativa=146), materias are checkboxes by código inside a modal
// dialog, turno is a combo/select control, días are checkboxes LU-SA,
// and results carry per-row hidden inputs hiddenLU/MA/MI/JU/VI/SA.
//
// Playwright role/label/text locators are used in preference to
// ASP.NET's auto-generated element `id`s, per this plan's guidance.
//
// SELECTORS confirmed live 2026-07-11 against the real DOM (with a valid
// UADE_START_URL param establishing acad context) for the top-level page.
// The materias-dialog trigger is an <a>, not a role="button" element, which
// is why an earlier role-based guess timed out. ASP.NET auto-generated ids
// are used directly here since they were confirmed stable against a live
// page_evaluate() dump, not guessed.
const OFRECIMIENTO_VALUES = {
  curricular: '145',
  optativa: '146',
};

const SELECTORS = {
  ofrecimientoRadio: (value) => `input[type="radio"][value="${value}"]`,
  materiasDialogTrigger: '#ContentPlaceHolder1_btnSeleccionarMaterias',
  turnoSelect: '#ContentPlaceHolder1_cboTurno',
  buscarButton: '#ContentPlaceHolder1_btnBuscar',
};

// Materia checkboxes use an ASP.NET id indexed by grid ROW POSITION
// (e.g. rptMaterias_0_chkSeleccionar_0), not by materia código — that id
// shifts depending on which row a given materia lands on for a given
// search, so it cannot be hardcoded. Confirmed live 2026-07-11.
//
// KNOWN LIMITATION (two failed attempts, fixed live 2026-07-11): both
// `tr` and `td` scoped by hasText matched 34 elements — the materias
// grid sits inside a wrapping layout table, so an OUTER `<td>`/`<tr>`
// also "contains" the target text via its nested descendant table, and
// `.first()` in DOM order picks that wrapper, not the leaf data row.
// Fix: search FROM the checkbox side instead (leaf elements, unambiguous
// by id substring `chkSeleccionar`), and filter to the one whose OWN
// nearest ancestor `<tr>` (not any wrapping ancestor further up) contains
// the materiaCodigo text — this direction of traversal can't fan out the
// way text-first search did.
function materiaCheckboxLocator(page, materiaCodigo) {
  return page.locator(
    `xpath=//input[@type="checkbox" and contains(@id, "chkSeleccionar")][ancestor::tr[1][contains(., "${materiaCodigo}")]]`
  );
}

// Día checkboxes have real ids confirmed live 2026-07-11 — NOT locatable
// by accessible name/label, since the displayed labels are 3-letter
// abbreviations (LUN/MAR/MIE/JUE/VIE/SAB) while FiltrosSchema uses 2-letter
// codes (LU/MA/MI/JU/VI/SA); an exact-match role/label locator would never
// match either alphabet, so this is a direct code -> id lookup instead.
const DIA_CHECKBOX_IDS = {
  LU: '#ContentPlaceHolder1_chkLunes',
  MA: '#ContentPlaceHolder1_chkMartes',
  MI: '#ContentPlaceHolder1_chkMiercoles',
  JU: '#ContentPlaceHolder1_chkJueves',
  VI: '#ContentPlaceHolder1_chkViernes',
  SA: '#ContentPlaceHolder1_chkSabado',
};

const DIA_HIDDEN_INPUT_IDS = {
  LU: 'hiddenLU',
  MA: 'hiddenMA',
  MI: 'hiddenMI',
  JU: 'hiddenJU',
  VI: 'hiddenVI',
  SA: 'hiddenSA',
};

/**
 * D-07's stale-`UADE_START_URL` detection signal, isolated from the DOM
 * query that feeds it (the query itself needs a live/mocked Playwright
 * `Page` and is out of this pure helper's unit-test scope). A per-user
 * signed entry link that has gone stale leaves the turno combo with zero
 * `<option>` elements — the Periodo/Carrera/Turno session state the portal
 * link is responsible for establishing never got populated.
 *
 * @param {number} turnoOptionCount
 * @returns {boolean}
 */
export function isStaleStartUrlSignal(turnoOptionCount) {
  return turnoOptionCount === 0;
}

/**
 * Resolves a human-provided turno label (e.g. "mañana", "MAÑANA") to the
 * `<option>` `value` attribute the live `#ContentPlaceHolder1_cboTurno`
 * select actually expects (opaque numeric ids like "10152" — confirmed
 * live 2026-07-11 via a read-only diagnostic: FiltrosSchema.turno is a
 * free-form label, but Playwright's `selectOption(string)` matches by
 * `value`, not visible text, so passing the label straight through never
 * matched any option). Case/accent-insensitive exact match against the
 * option's trimmed text — `localeCompare` with `sensitivity: 'base'`
 * treats "mañana" and "MAÑANA" as equal without needing manual
 * normalization.
 *
 * @param {{ value: string, text: string }[]} options
 * @param {string} turnoLabel
 * @returns {string} the matching option's `value`
 */
export function resolveTurnoOptionValue(options, turnoLabel) {
  const match = options.find((opt) => opt.text.localeCompare(turnoLabel, 'es', { sensitivity: 'base' }) === 0);
  if (!match) {
    const available = options.map((opt) => opt.text).join(', ');
    throw new Error(`turno "${turnoLabel}" not found among available options: ${available}`);
  }
  return match.value;
}

/**
 * Drives an active UADE search page against the given filtros, from the
 * ofrecimiento radio through clicking the día checkboxes — everything
 * except the final "Buscar" click, which the caller triggers separately
 * so it can race it against `page.waitForResponse`.
 *
 * @param {import('playwright').Page} page
 * @param {import('zod').infer<typeof FiltrosSchema>} filtros
 */
async function driveSearchForm(page, filtros) {
  const ofrecimientoValue = OFRECIMIENTO_VALUES[filtros.ofrecimiento];
  await page.locator(SELECTORS.ofrecimientoRadio(ofrecimientoValue)).check();

  await page.locator(SELECTORS.materiasDialogTrigger).click();

  await materiaCheckboxLocator(page, filtros.materiaCodigo).check();

  // The materias picker is a jQuery UI modal dialog that stays open after
  // checking a materia — its overlay intercepts clicks on the turno/día
  // controls underneath until explicitly closed. Confirmed live 2026-07-11:
  // this theme hides the titlebar "X" icon via CSS (DOM element exists but
  // is not visible) — the actual close control is the "Cerrar" text button.
  const cerrarButton = page.getByRole('button', { name: 'Cerrar', exact: true });
  await cerrarButton.click();
  await cerrarButton.waitFor({ state: 'hidden' });

  const turnoOptions = await page
    .locator(`${SELECTORS.turnoSelect} option`)
    .evaluateAll((opts) => opts.map((o) => ({ value: o.value, text: o.textContent.trim() })));
  const turnoValue = resolveTurnoOptionValue(turnoOptions, filtros.turno);
  await page.locator(SELECTORS.turnoSelect).selectOption(turnoValue);

  for (const dia of filtros.dias) {
    await page.locator(DIA_CHECKBOX_IDS[dia]).check();
  }
}

/**
 * Reads back the post-postback DOM/hidden-input state so it can be
 * compared against the submitted filtros before the response is trusted
 * as authoritative (SEARCH-04).
 *
 * @param {import('playwright').Page} page
 * @param {string} expectedMateriaCodigo the submitted materiaCodigo, used to
 *   confirm its presence in the checked row's text as an exact token (see
 *   the materiaCodigo extraction note below for why this can't be a blind
 *   generic regex match)
 * @returns {Promise<{ materiaCodigo: string|null, ofrecimiento: string|null, turno: string|null, dias: string[] }>}
 */
async function readReflectedFormState(page, expectedMateriaCodigo) {
  // NOTE: hiddenLU/MA/MI/JU/VI/SA (DIA_HIDDEN_INPUT_IDS) belong to RESULTS
  // rows (Plan 01-02's concern — which días a given result section runs),
  // not the search form itself. Reflected form state is read directly off
  // the same día checkboxes driveSearchForm() checked, confirmed live
  // 2026-07-11.
  const dias = [];
  for (const [dia, selector] of Object.entries(DIA_CHECKBOX_IDS)) {
    const isChecked = await page.locator(selector).isChecked().catch(() => false);
    if (isChecked) {
      dias.push(dia);
    }
  }

  const checkedRadio = page.locator('input[type="radio"]:checked');
  const checkedRadioValue = (await checkedRadio.count()) > 0 ? await checkedRadio.first().getAttribute('value') : null;
  const ofrecimiento =
    Object.entries(OFRECIMIENTO_VALUES).find(([, value]) => value === checkedRadioValue)?.[0] ?? null;

  // Read back the selected option's visible TEXT, not its `value` — the
  // live select's values are opaque numeric ids (e.g. "10152"), while
  // `filtros.turno` is the human label ("mañana") `resolveTurnoOptionValue`
  // resolved from at submit time. Comparing values here would never match
  // the submitted label (see resolveTurnoOptionValue's docstring).
  let turno = null;
  const turnoSelect = page.locator(SELECTORS.turnoSelect);
  if ((await turnoSelect.count()) > 0) {
    turno = await turnoSelect.locator('option:checked').first().textContent();
    turno = turno?.trim() ?? null;
  }

  // Materia checkboxes have no data-materia-codigo attribute and no stable
  // id (see materiaCheckboxLocator) — find the CHECKED materia-grid
  // checkbox specifically (scoped by its confirmed id substring, not any
  // checkbox on the page — día checkboxes are also :checked and would
  // otherwise collide here) and confirm the código's presence in its row's
  // text. Climb via xpath ancestor::tr[1] rather than a `tr`-with-hasText/
  // has locator, which matches every wrapping ancestor row, not just the
  // leaf.
  //
  // KNOWN LIMITATION (fixed live 2026-07-11, two attempts): row text
  // concatenates the grid's "Or." (order/row number) column directly
  // against the código with NO separator or whitespace (e.g. row number
  // "5" + código "3.1.050" reads as "53.1.050"). A generic
  // `/\d+\.\d+\.\d+/` match is greedy and swallows that leading digit into
  // the first segment, producing the WRONG code. A first fix attempt
  // required a non-digit boundary immediately before the expected code —
  // but the row-number digit is ALWAYS immediately adjacent with no
  // separator in this site's markup, so that boundary can never be
  // satisfied for a genuine match either, causing every real match to be
  // rejected. Since the checked checkbox's row is already the one
  // materiaCheckboxLocator specifically selected for this exact code, a
  // plain substring check is sufficient here — this isn't re-deriving an
  // unknown value, just confirming the expected code's presence survived
  // the postback in the reflected DOM.
  let materiaCodigo = null;
  const checkedMateriaCheckbox = page.locator('input[type="checkbox"][id*="chkSeleccionar"]:checked');
  if ((await checkedMateriaCheckbox.count()) > 0) {
    const row = checkedMateriaCheckbox.first().locator('xpath=ancestor::tr[1]');
    const rowText = await row.textContent();
    materiaCodigo = rowText?.includes(expectedMateriaCodigo) ? expectedMateriaCodigo : null;
  }

  return { materiaCodigo, ofrecimiento, turno, dias };
}

/**
 * Pure function: returns `true` only when every reflected value in
 * `reflectedState` matches what was submitted in `filtros`. Never treats
 * a mismatch as "0 vacancies" — the caller is responsible for mapping a
 * `false` result to a `search_failed` status (SEARCH-04).
 *
 * @param {{ materiaCodigo: string|null, ofrecimiento: string|null, turno: string|null, dias: string[] }} reflectedState
 * @param {import('zod').infer<typeof FiltrosSchema>} filtros
 * @returns {boolean}
 */
export function verifyPostbackMatchesQuery(reflectedState, filtros) {
  if (!reflectedState) {
    return false;
  }

  if (reflectedState.materiaCodigo !== filtros.materiaCodigo) {
    return false;
  }

  if (reflectedState.ofrecimiento !== filtros.ofrecimiento) {
    return false;
  }

  // Case/accent-insensitive: reflectedState.turno is the live select's
  // visible option text (e.g. "MAÑANA"), filtros.turno is the human-typed
  // label (e.g. "mañana") — see resolveTurnoOptionValue's docstring.
  if (reflectedState.turno == null || reflectedState.turno.localeCompare(filtros.turno, 'es', { sensitivity: 'base' }) !== 0) {
    return false;
  }

  const reflectedDias = [...reflectedState.dias].sort();
  const submittedDias = [...filtros.dias].sort();

  if (reflectedDias.length !== submittedDias.length) {
    return false;
  }

  return reflectedDias.every((dia, index) => dia === submittedDias[index]);
}

function isAuthChallengeError(err) {
  const message = err?.message ?? '';
  return /401|unauthorized|ERR_INVALID_AUTH_CREDENTIALS/i.test(message);
}

/**
 * Drives the live UADE search form for the given filtros and verifies the
 * postback matches the submitted query before trusting the response.
 *
 * @param {import('playwright').BrowserContext} context
 * @param {import('zod').infer<typeof FiltrosSchema>} filtros
 * @param {{ startUrl?: string }} [options] - per-call start URL (e.g. a
 *   per-user decrypted `uadeStartUrl`, Plan 02-02). Falls back to the
 *   process-global `UADE_START_URL` env var when omitted, preserving
 *   `src/cli.js`'s existing single-user call site unchanged.
 * @returns {Promise<{ status: 'invalid_credentials' } | { status: 'rate_limited' } | { status: 'stale_start_url' } | { status: 'search_failed', reason: string } | { status: 'verified', html: string }>}
 */
export async function runSearch(context, filtros, { startUrl } = {}) {
  const parsedFiltros = FiltrosSchema.parse(filtros);
  const resolvedStartUrl = startUrl ?? loadEnv().UADE_START_URL;
  if (!resolvedStartUrl) {
    throw new Error('runSearch: no startUrl provided and UADE_START_URL is not set');
  }

  const page = await context.newPage();

  let navigationResponse;
  try {
    navigationResponse = await page.goto(resolvedStartUrl, { waitUntil: 'domcontentloaded' });
  } catch (err) {
    if (isAuthChallengeError(err)) {
      logger.info({ event: 'auth_challenge_error' }, 'Navigation failed with an auth challenge');
      return { status: 'invalid_credentials' };
    }
    throw err;
  }

  if (navigationResponse && navigationResponse.status() === 401) {
    logger.info({ event: 'auth_rejected', status: 401 }, 'Basic Auth challenge rejected by the UADE site');
    return { status: 'invalid_credentials' };
  }

  if (navigationResponse && navigationResponse.status() === 429) {
    logger.info({ event: 'rate_limited' }, 'Basic Auth challenge returned 429 on navigation');
    return { status: 'rate_limited' };
  }

  const turnoOptionCount = await page
    .locator(SELECTORS.turnoSelect)
    .locator('option')
    .count()
    .catch(() => 0);
  if (isStaleStartUrlSignal(turnoOptionCount)) {
    logger.info(
      { event: 'stale_start_url_detected' },
      'Turno combo is empty — UADE_START_URL is likely stale for this user',
    );
    return { status: 'stale_start_url' };
  }

  // The portal's signed link may redirect through intermediate pages before
  // landing on the search page — resolve the pathname actually reached
  // rather than assuming it matches SEARCH_URL verbatim (query string may
  // differ, e.g. carrying periodo/carrera state).
  const searchPagePathname = new URL(page.url()).pathname;

  try {
    await driveSearchForm(page, parsedFiltros);
  } catch (err) {
    logger.error({ event: 'form_drive_failed', message: err.message }, 'Failed to drive the search form');
    return { status: 'search_failed', reason: 'form_drive_failed' };
  }

  const responsePromise = page.waitForResponse((response) => {
    let responsePathname;
    try {
      responsePathname = new URL(response.url()).pathname;
    } catch {
      return false;
    }
    if (responsePathname !== searchPagePathname) {
      return false;
    }
    return response.request().method() === 'POST';
  });

  const buscarButton = page.locator(SELECTORS.buscarButton);

  let postbackResponse;
  try {
    [postbackResponse] = await Promise.all([responsePromise, buscarButton.click()]);
  } catch (err) {
    logger.error({ event: 'postback_wait_failed', message: err.message }, 'Timed out waiting for the search postback response');
    return { status: 'search_failed', reason: 'postback_timeout' };
  }

  if (postbackResponse.status() === 401) {
    logger.info({ event: 'auth_rejected', status: 401 }, 'Basic Auth challenge rejected on postback');
    return { status: 'invalid_credentials' };
  } else if (postbackResponse.status() === 429) {
    logger.info({ event: 'rate_limited' }, 'Basic Auth challenge returned 429 on postback');
    return { status: 'rate_limited' };
  }

  const reflectedState = await readReflectedFormState(page, parsedFiltros.materiaCodigo);
  const matches = verifyPostbackMatchesQuery(reflectedState, parsedFiltros);

  if (!matches) {
    logger.warn(
      { event: 'postback_mismatch', reflectedState, submitted: parsedFiltros },
      'Postback reflected state does not match submitted filtros'
    );
    return { status: 'search_failed', reason: 'postback_mismatch' };
  }

  const html = await page.content();
  return { status: 'verified', html };
}
