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
 * "¡INSCRIBITE!" link -- never clicks it (a bootbox modal intercepts that
 * click, and the full target URL is already sitting in the attribute).
 * Returns `null` when that specific link isn't present, even if other
 * `data-tipolink` links (e.g. the MRI modality) are on the page.
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
    // Never retried or evaded within this same call.
    if (isStuckOnMicrosoftDomain(page.url())) {
      logger.info(
        { event: 'sso_mfa_detected' },
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

    const startUrl = await extractInscripcionLink(page);
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
