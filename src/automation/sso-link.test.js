import { test, describe, after } from 'node:test';
import assert from 'node:assert/strict';
import { toMicrosoftEmail, isStuckOnMicrosoftDomain, isValidStartUrl, extractInscripcionLink, confirmInscripcionLink } from './sso-link.js';
import { getBrowser } from './browser.js';

// extractInscripcionLink() reuses the shared headless Chromium instance --
// same reason as src/automation/parse-results.test.js's after() hook:
// without an explicit close, that Chromium process outlives this test run
// and keeps `node --test` from exiting on its own.
after(async () => {
  const browser = await getBrowser();
  await browser.close();
});

describe('toMicrosoftEmail', () => {
  test('appends @uade.edu.ar when username has no @', () => {
    assert.equal(toMicrosoftEmail('jperez'), 'jperez@uade.edu.ar');
  });

  test('returns the username unchanged when it already has an @', () => {
    assert.equal(toMicrosoftEmail('jperez@otrodominio.com'), 'jperez@otrodominio.com');
  });
});

describe('isStuckOnMicrosoftDomain', () => {
  test('returns true for a login.microsoftonline.com URL', () => {
    assert.equal(
      isStuckOnMicrosoftDomain('https://login.microsoftonline.com/tenant-id/oauth2/v2.0/authorize?x=1'),
      true,
    );
  });

  test('returns false for a non-Microsoft URL', () => {
    assert.equal(isStuckOnMicrosoftDomain('https://inscripciones.uade.edu.ar/'), false);
  });

  test('returns false for a URL on an unrelated Microsoft-looking subdomain', () => {
    assert.equal(isStuckOnMicrosoftDomain('https://notlogin.microsoftonline.com.evil.example/'), false);
  });

  test('returns false for undefined', () => {
    assert.equal(isStuckOnMicrosoftDomain(undefined), false);
  });
});

describe('isValidStartUrl', () => {
  test('returns true for a non-empty string containing param=', () => {
    assert.equal(isValidStartUrl('https://inscripcionespia.uade.edu.ar/x?param=abc'), true);
  });

  test('returns false for null', () => {
    assert.equal(isValidStartUrl(null), false);
  });

  test('returns false for undefined', () => {
    assert.equal(isValidStartUrl(undefined), false);
  });

  test('returns false for an empty string', () => {
    assert.equal(isValidStartUrl(''), false);
  });

  test('returns false for a URL without param=', () => {
    assert.equal(isValidStartUrl('https://inscripcionespia.uade.edu.ar/x?otro=abc'), false);
  });
});

describe('extractInscripcionLink', () => {
  test('reads data-linkid from the InscripcionAsignatura link', async () => {
    const browser = await getBrowser();
    const context = await browser.newContext();
    try {
      const page = await context.newPage();
      await page.setContent(`<!DOCTYPE html><html><body>
        <a class="link-inscripciones inscribite" data-tipolink="InscripcionAsignatura" data-linkid="https://inscripcionespia.uade.edu.ar/x?param=secret123">¡INSCRIBITE!</a>
      </body></html>`);

      const linkId = await extractInscripcionLink(page);
      assert.equal(linkId, 'https://inscripcionespia.uade.edu.ar/x?param=secret123');
    } finally {
      await context.close();
    }
  });

  test('reads the InscripcionAsignatura link even when another data-tipolink link is present', async () => {
    const browser = await getBrowser();
    const context = await browser.newContext();
    try {
      const page = await context.newPage();
      await page.setContent(`<!DOCTYPE html><html><body>
        <a class="link-inscripciones inscribite" data-tipolink="MRI" data-linkid="https://inscripcionespia.uade.edu.ar/x?param=mri-secret">¡INSCRIBITE!</a>
        <a class="link-inscripciones inscribite" data-tipolink="InscripcionAsignatura" data-linkid="https://inscripcionespia.uade.edu.ar/x?param=correct-secret">¡INSCRIBITE!</a>
      </body></html>`);

      const linkId = await extractInscripcionLink(page);
      assert.equal(linkId, 'https://inscripcionespia.uade.edu.ar/x?param=correct-secret');
    } finally {
      await context.close();
    }
  });

  test('returns null when no InscripcionAsignatura link is present on the page', async () => {
    const browser = await getBrowser();
    const context = await browser.newContext();
    try {
      const page = await context.newPage();
      await page.setContent('<!DOCTYPE html><html><body><p>sin links de inscripción</p></body></html>');

      const linkId = await extractInscripcionLink(page);
      assert.equal(linkId, null);
    } finally {
      await context.close();
    }
  });
});

describe('confirmInscripcionLink', () => {
  // Reproduces the real markup's shape: an href="#" target="_blank" link
  // whose click handler shows a bootbox-style confirm modal first, and only
  // opens the real destination in a new page once that modal is confirmed --
  // this is the "activation" side effect that reading data-linkid alone
  // skips (confirmed live 2026-07-12, see obtainStartUrl's docstring).
  const PAGE_WITH_BOOTBOX_GATE = `<!DOCTYPE html><html><body>
    <a class="link-inscripciones inscribite" data-tipolink="InscripcionAsignatura"
       data-linkid="https://inscripcionespia.uade.edu.ar/x?param=secret123"
       href="#" target="_blank" onclick="document.getElementById('modal').style.display='block'; return false;">¡INSCRIBITE!</a>
    <div id="modal" class="bootbox" style="display:none;">
      <button class="btn-primary" onclick="window.open(document.querySelector('.inscribite').getAttribute('data-linkid'), '_blank'); document.getElementById('modal').style.display='none';">Confirmar</button>
    </div>
  </body></html>`;

  test('clicks the link, confirms the bootbox modal, and returns the popup URL', async () => {
    const browser = await getBrowser();
    const context = await browser.newContext();
    try {
      const page = await context.newPage();
      await page.setContent(PAGE_WITH_BOOTBOX_GATE);

      const startUrl = await confirmInscripcionLink(context, page);

      assert.equal(startUrl, 'https://inscripcionespia.uade.edu.ar/x?param=secret123');
    } finally {
      await context.close();
    }
  });

  test('falls back to reading data-linkid when the click opens no popup and no modal appears', async () => {
    const browser = await getBrowser();
    const context = await browser.newContext();
    try {
      const page = await context.newPage();
      await page.setContent(`<!DOCTYPE html><html><body>
        <a class="link-inscripciones inscribite" data-tipolink="InscripcionAsignatura"
           data-linkid="https://inscripcionespia.uade.edu.ar/x?param=fallback-secret"
           href="#" onclick="return false;">¡INSCRIBITE!</a>
      </body></html>`);

      const startUrl = await confirmInscripcionLink(context, page);

      assert.equal(startUrl, 'https://inscripcionespia.uade.edu.ar/x?param=fallback-secret');
    } finally {
      await context.close();
    }
  });

  test('returns null when no InscripcionAsignatura link is present', async () => {
    const browser = await getBrowser();
    const context = await browser.newContext();
    try {
      const page = await context.newPage();
      await page.setContent('<!DOCTYPE html><html><body><p>sin links de inscripción</p></body></html>');

      const startUrl = await confirmInscripcionLink(context, page);

      assert.equal(startUrl, null);
    } finally {
      await context.close();
    }
  });
});
