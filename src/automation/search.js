import { FiltrosSchema } from '../schemas.js';
import logger from '../logger.js';

export const SEARCH_URL = 'https://inscripcionespia.uade.edu.ar/InscripcionClaseBuscar.aspx';

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
// KNOWN LIMITATION: the exact accessible names for the "open materias
// dialog" trigger, the turno control's label, and the día checkbox
// labels were not independently re-verified against the live DOM at
// implementation time (no live UADE credentials were available in this
// environment). These constants are the best-effort selectors derived
// from PROJECT.md's documented structure; PLAN.md's Task 3 human-check
// is the point where a developer with real UADE credentials confirms
// (and corrects, if needed) these values against the real site.
const OFRECIMIENTO_VALUES = {
  curricular: '145',
  optativa: '146',
};

const SELECTORS = {
  ofrecimientoRadio: (value) => `input[type="radio"][value="${value}"]`,
  materiasDialogTrigger: { role: 'button', name: /materias/i },
  materiaCheckbox: (materiaCodigo) => ({
    role: 'checkbox',
    name: new RegExp(materiaCodigo.replace(/\./g, '\\.')),
  }),
  turnoLabel: /turno/i,
  diaCheckbox: (dia) => ({ role: 'checkbox', name: new RegExp(`^${dia}$`, 'i') }),
  buscarButton: { role: 'button', name: /buscar/i },
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

  await page
    .getByRole(SELECTORS.materiasDialogTrigger.role, { name: SELECTORS.materiasDialogTrigger.name })
    .click();

  const materiaLocator = SELECTORS.materiaCheckbox(filtros.materiaCodigo);
  await page.getByRole(materiaLocator.role, { name: materiaLocator.name }).check();

  await page.getByLabel(SELECTORS.turnoLabel).selectOption(filtros.turno);

  for (const dia of filtros.dias) {
    const diaLocator = SELECTORS.diaCheckbox(dia);
    await page.getByRole(diaLocator.role, { name: diaLocator.name }).check();
  }
}

/**
 * Reads back the post-postback DOM/hidden-input state so it can be
 * compared against the submitted filtros before the response is trusted
 * as authoritative (SEARCH-04).
 *
 * @param {import('playwright').Page} page
 * @returns {Promise<{ materiaCodigo: string|null, ofrecimiento: string|null, turno: string|null, dias: string[] }>}
 */
async function readReflectedFormState(page) {
  const dias = [];
  for (const [dia, hiddenId] of Object.entries(DIA_HIDDEN_INPUT_IDS)) {
    const count = await page.locator(`#${hiddenId}`).count();
    if (count > 0) {
      dias.push(dia);
    }
  }

  const checkedRadio = page.locator('input[type="radio"]:checked');
  const checkedRadioValue = (await checkedRadio.count()) > 0 ? await checkedRadio.first().getAttribute('value') : null;
  const ofrecimiento =
    Object.entries(OFRECIMIENTO_VALUES).find(([, value]) => value === checkedRadioValue)?.[0] ?? null;

  let turno = null;
  const turnoSelect = page.getByLabel(SELECTORS.turnoLabel);
  if ((await turnoSelect.count()) > 0) {
    turno = await turnoSelect.first().inputValue();
  }

  let materiaCodigo = null;
  const checkedMateria = page.locator('input[type="checkbox"]:checked');
  if ((await checkedMateria.count()) > 0) {
    materiaCodigo = (await checkedMateria.first().getAttribute('data-materia-codigo')) ?? null;
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

  if (reflectedState.turno !== filtros.turno) {
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
 * @returns {Promise<{ status: 'invalid_credentials' } | { status: 'search_failed', reason: string } | { status: 'verified', html: string }>}
 */
export async function runSearch(context, filtros) {
  const parsedFiltros = FiltrosSchema.parse(filtros);

  const page = await context.newPage();

  let navigationResponse;
  try {
    navigationResponse = await page.goto(SEARCH_URL, { waitUntil: 'domcontentloaded' });
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

  try {
    await driveSearchForm(page, parsedFiltros);
  } catch (err) {
    logger.error({ event: 'form_drive_failed', message: err.message }, 'Failed to drive the search form');
    return { status: 'search_failed', reason: 'form_drive_failed' };
  }

  const responsePromise = page.waitForResponse((response) => {
    if (response.url() !== SEARCH_URL) {
      return false;
    }
    return response.request().method() === 'POST';
  });

  const buscarButton = page.getByRole(SELECTORS.buscarButton.role, { name: SELECTORS.buscarButton.name });

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
  }

  const reflectedState = await readReflectedFormState(page);
  const matches = verifyPostbackMatchesQuery(reflectedState, parsedFiltros);

  if (!matches) {
    logger.warn({ event: 'postback_mismatch' }, 'Postback reflected state does not match submitted filtros');
    return { status: 'search_failed', reason: 'postback_mismatch' };
  }

  const html = await page.content();
  return { status: 'verified', html };
}
