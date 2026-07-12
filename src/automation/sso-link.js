import logger from '../logger.js';

// The separate enrollment portal that fronts Microsoft/Azure AD SSO -- NOT
// inscripcionespia.uade.edu.ar, which the rest of the bot already automates
// via Basic Auth. This portal lands on its own /Account/Login page first;
// it does not redirect straight to Microsoft (confirmed live, spike 001 --
// see .claude/skills/spike-findings-uade-bot/references/uade-sso-link-automation.md).
export const ENROLLMENT_PORTAL_URL = 'https://inscripciones.uade.edu.ar/';

const MICROSOFT_LOGIN_HOST = 'login.microsoftonline.com';

// Static, long-stable selectors confirmed live against the real DOM (spike
// 001). Microsoft's own field/button ids and UADE's Bootstrap tab/inscribite
// markup have no public contract and can change without notice -- same
// reactive-maintenance regime PROJECT.md already documents for
// inscripcionespia.uade.edu.ar's own selectors.
const SELECTORS = {
  microsoftEmail: 'input[name="loginfmt"], #i0116',
  // Reused for both the "Siguiente" step and the "Iniciar sesión" step --
  // Microsoft assigns the same id to both buttons across its two-step form,
  // and again to the "Stay signed in?" interstitial's "Yes" button.
  microsoftContinueButton: '#idSIButton9',
  microsoftPassword: 'input[name="passwd"], #i0118',
  bootstrapTab: 'a[data-toggle="tab"][href="#menu3"]',
  inscripcionLink: 'a.inscribite[data-tipolink="InscripcionAsignatura"]',
  // The link's own click handler shows a bootbox.js confirmation before
  // doing anything -- confirmed live 2026-07-12 (a bootbox modal intercepted
  // the click). bootbox renders its primary/confirm button with this class;
  // best-effort broadened with a text fallback since the exact bootbox
  // config (button label) hasn't been observed directly yet.
  bootboxConfirmButton: '.bootbox .btn-primary, .bootbox .btn-confirm',
};

/**
 * Adds `@uade.edu.ar` to `username` only if it doesn't already carry an
 * `@` -- the Microsoft login form expects a full email address, which may
 * differ from the bare `UADE_USERNAME` used for Basic Auth against
 * inscripcionespia.uade.edu.ar.
 *
 * @param {string} username
 * @returns {string}
 */
export function toMicrosoftEmail(username) {
  return username.includes('@') ? username : `${username}@uade.edu.ar`;
}

/**
 * True only when `url`'s host is exactly `login.microsoftonline.com` --
 * the terminal signal AUTOLINK-02 uses to detect MFA/an extra login step
 * (obtainStartUrl never retries or tries to evade it). False for any other
 * URL (including look-alike hosts) or for `undefined`/an unparseable value.
 *
 * @param {string | undefined} url
 * @returns {boolean}
 */
export function isStuckOnMicrosoftDomain(url) {
  if (!url) {
    return false;
  }
  try {
    return new URL(url).host === MICROSOFT_LOGIN_HOST;
  } catch {
    return false;
  }
}

/**
 * True only for a non-empty string containing `param=` -- the shape a
 * genuine `data-linkid` inscripción URL always carries.
 *
 * @param {unknown} candidate
 * @returns {boolean}
 */
export function isValidStartUrl(candidate) {
  return typeof candidate === 'string' && candidate.length > 0 && candidate.includes('param=');
}

/**
 * Reads the `data-linkid` attribute off the `InscripcionAsignatura`
 * "¡INSCRIBITE!" link without clicking it. `data-linkid` always carries a
 * syntactically complete param= URL, but confirmed live 2026-07-12 that
 * using it directly (skipping the click) produces a search session where
 * the materias dialog never populates the expected row (`form_drive_failed`,
 * a `locator.check` timeout) -- the link's own click handler likely performs
 * a server-side "activation" step (behind the bootbox confirmation) that
 * this attribute-only read skips. Kept as a last-resort fallback inside
 * `confirmInscripcionLink`, not as the primary path.
 *
 * @param {import('playwright').Page} page
 * @returns {Promise<string | null>}
 */
export async function extractInscripcionLink(page) {
  const link = page.locator(SELECTORS.inscripcionLink).first();
  const count = await link.count();
  if (count === 0) {
    return null;
  }
  return link.getAttribute('data-linkid');
}

/**
 * Clicks the `InscripcionAsignatura` "¡INSCRIBITE!" link and confirms the
 * bootbox modal its handler shows, since the resulting search session only
 * works when the click's own (likely server-side activation) side effect
 * has actually run -- reading `data-linkid` alone is not equivalent (see
 * `extractInscripcionLink`'s docstring). The link carries `target="_blank"`,
 * so the real destination is expected to open in a new page/tab.
 *
 * @param {import('playwright').BrowserContext} context
 * @param {import('playwright').Page} page
 * @returns {Promise<string | null>}
 */
export async function confirmInscripcionLink(context, page) {
  const link = page.locator(SELECTORS.inscripcionLink).first();
  if ((await link.count()) === 0) {
    return null;
  }

  // Diagnostic only, never sensitive: confirms the selector is really
  // matching the InscripcionAsignatura link and not some other listing
  // (e.g. MRI) that happens to share the .inscribite class -- doubted live
  // 2026-07-12 when a materia catalog mismatch surfaced. tipolink/count are
  // plain attribute values, not URLs or credentials.
  const allInscribiteLinks = page.locator('a.inscribite');
  const [totalInscribiteLinks, matchedTipolink] = await Promise.all([
    allInscribiteLinks.count().catch(() => -1),
    link.getAttribute('data-tipolink').catch(() => null),
  ]);
  logger.info(
    { event: 'sso_inscripcion_link_selected', totalInscribiteLinks, matchedTipolink },
    'Selected the inscripción link to click',
  );

  const popupPromise = context.waitForEvent('page', { timeout: 5000 }).catch(() => null);
  await link.click({ timeout: 8000 }).catch((err) => {
    logger.info({ event: 'sso_inscripcion_link_click_failed', errorName: err.name }, 'Click on the inscripción link failed');
  });

  try {
    await page.waitForSelector('.bootbox', { timeout: 5000 });
    await page.locator(SELECTORS.bootboxConfirmButton).first().click({ timeout: 5000 });
    logger.info({ event: 'sso_bootbox_confirmed' }, 'Confirmed the bootbox modal shown by the inscripción link');
  } catch {
    // No modal appeared (or it already resolved) -- continue either way.
  }

  const popup = await popupPromise;
  if (popup) {
    await popup.waitForLoadState('domcontentloaded').catch(() => {});
    const popupUrl = popup.url();
    await popup.close().catch(() => {});
    if (isValidStartUrl(popupUrl)) {
      return popupUrl;
    }
  }

  // No popup opened (or it didn't land on a param= URL) -- fall back to the
  // attribute read. Still gives the "click + confirm" side effect a chance
  // to have already run before this read, even though it's not itself the
  // activation trigger.
  return extractInscripcionLink(page);
}

/**
 * Reproduces the full Microsoft/Azure AD SSO login flow confirmed live in
 * spike 001 (portal -> "Iniciar sesión" -> Microsoft two-step form -> "Stay
 * signed in?" interstitial -> Bootstrap tab -> data-linkid) and returns
 * exactly one of the three documented outcomes. Never throws to the caller
 * -- any unanticipated error is converted to `{ status: 'failed', reason:
 * 'unexpected_error' }`.
 *
 * CRED-04 discipline: `password` is only ever handed directly to
 * Playwright's `fill()`; it is never assigned to any variable that outlives
 * this call and never appears in a `logger.*` argument. Same for the
 * Microsoft email built from `username` and the `startUrl` this function
 * extracts -- only fixed event names and, where relevant, `err.name` are
 * logged (AUTOLINK-04).
 *
 * @param {import('playwright').BrowserContext} context
 * @param {{ username: string, password: string }} credentials
 * @returns {Promise<
 *   | { status: 'success', startUrl: string }
 *   | { status: 'mfa_required' }
 *   | { status: 'failed', reason: string }
 * >}
 */
export async function obtainStartUrl(context, { username, password }) {
  try {
    const page = await context.newPage();

    await page.goto(ENROLLMENT_PORTAL_URL, { waitUntil: 'domcontentloaded' });

    if (!isStuckOnMicrosoftDomain(page.url())) {
      try {
        await page
          .getByRole('button', { name: /iniciar sesi[oó]n/i })
          .or(page.getByRole('link', { name: /iniciar sesi[oó]n/i }))
          .first()
          .click({ timeout: 8000 });
        // Best-effort: the portal's own login button triggers a
        // server-side redirect to Microsoft, but that's not guaranteed to
        // succeed (e.g. an already-authenticated session). Don't throw if
        // it never arrives -- the isStuckOnMicrosoftDomain check right
        // below re-evaluates the actual outcome either way.
        await page.waitForURL((url) => isStuckOnMicrosoftDomain(url.href), { timeout: 10000 }).catch(() => {});
      } catch (err) {
        logger.info(
          { event: 'sso_portal_login_button_not_found', errorName: err.name },
          'Could not find/click the enrollment portal\'s own "Iniciar sesión" control',
        );
      }
    }

    if (isStuckOnMicrosoftDomain(page.url())) {
      const microsoftEmail = toMicrosoftEmail(username);
      try {
        await page.fill(SELECTORS.microsoftEmail, microsoftEmail, { timeout: 5000 });
        await page.click(SELECTORS.microsoftContinueButton, { timeout: 5000 });
        await page.fill(SELECTORS.microsoftPassword, password, { timeout: 8000 });
        await page.click(SELECTORS.microsoftContinueButton, { timeout: 5000 });
      } catch (err) {
        logger.warn(
          { event: 'sso_credential_fill_failed', errorName: err.name },
          'Failed to fill/submit the Microsoft login form',
        );
        return { status: 'failed', reason: 'credential_fill_failed' };
      }

      // Microsoft's "Stay signed in?" (KMSI) interstitial reuses the same
      // button id -- best-effort: some accounts never show it. Don't check
      // page.url() immediately after clicking; that races the redirect
      // back to the enrollment portal and reads the stale Microsoft URL
      // (confirmed live, spike 001).
      try {
        await page.waitForSelector(SELECTORS.microsoftContinueButton, { timeout: 8000 });
        await page.click(SELECTORS.microsoftContinueButton, { timeout: 5000 });
        await page.waitForURL((url) => !isStuckOnMicrosoftDomain(url.href), { timeout: 10000 }).catch(() => {});
      } catch {
        // No interstitial appeared (or it already resolved) -- continue.
      }
    }

    // AUTOLINK-02: still on a Microsoft login domain after the sign-in
    // attempt is the terminal signal for MFA/an extra verification step.
    // Never retried or evaded within this same call. Diagnostic-only, never
    // sensitive: Microsoft's own displayed error text (e.g. "your account or
    // password is incorrect", "verify your identity") -- never the password
    // itself -- disambiguates a real extra-verification step from a mundane
    // wrong-password/rate-limit error that also leaves the page stuck here.
    if (isStuckOnMicrosoftDomain(page.url())) {
      const microsoftErrorText = await page
        .locator('[role="alert"], .alert-error, #usernameError, #passwordError')
        .first()
        .innerText({ timeout: 2000 })
        .catch(() => null);
      logger.info(
        { event: 'sso_mfa_detected', microsoftErrorText },
        'Still on a Microsoft login domain after the sign-in attempt -- treating as MFA/extra step required',
      );
      return { status: 'mfa_required' };
    }

    try {
      await page.click(SELECTORS.bootstrapTab, { timeout: 8000 });
      await page.waitForTimeout(500); // let Bootstrap's tab-switch animation/DOM update settle
    } catch (err) {
      logger.warn(
        { event: 'sso_bootstrap_tab_failed', errorName: err.name },
        'Failed to activate the Bootstrap tab holding the inscripción link',
      );
    }

    const startUrl = await confirmInscripcionLink(context, page);
    if (!isValidStartUrl(startUrl)) {
      logger.info(
        { event: 'sso_link_not_found' },
        'Could not extract a valid inscripción link after completing SSO login',
      );
      return { status: 'failed', reason: 'link_not_found' };
    }

    return { status: 'success', startUrl };
  } catch (err) {
    logger.error(
      { event: 'sso_unexpected_error', errorName: err.name },
      'Unexpected error during SSO link automation',
    );
    return { status: 'failed', reason: 'unexpected_error' };
  }
}
