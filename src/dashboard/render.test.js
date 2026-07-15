import assert from 'node:assert/strict';
import { test } from 'node:test';

import { createRefreshController, escapeHtml, renderDashboardPage, renderLoginPage, renderSafeError } from './render.js';

const snapshot = {
  generatedAt: 1_750_000_000_000,
  health: {
    activeAccounts: 1,
    pausedAccounts: { total: 1, breakdown: [{ code: 'rate_limited', label: 'Límite de UADE', count: 1 }] },
    jobs: { total: 2, active: 1, manuallyPaused: 1 },
    lastSuccessfulPollAt: 1_749_999_990_000,
  },
  accounts: [{
    discordUserId: 'user-1',
    displayName: 'Operador <script>alert(1)</script>',
    status: { code: 'active', label: 'Activa', tone: 'healthy' },
    jobCount: 1,
    lastPolledAt: 1_749_999_995_000,
    jobs: [{
      jobId: 7,
      label: 'Álgebra "A"',
      status: { code: 'active', label: 'Activa', tone: 'healthy' },
      lastPolledAt: 1_749_999_995_000,
      outcome: { code: 'found', label: 'Vacantes encontradas', tone: 'healthy', vacancyCount: 1, totalCupos: 2 },
      filters: { materiaCodigo: '1234', turno: 'Noche', ofrecimiento: 'Curricular', dias: ['Lunes'], sedesExcluidasLabel: 'Sin exclusiones' },
      history: [{ id: 11, recordedAt: 1_749_999_995_000, outcome: { code: 'found', label: 'Vacantes encontradas', tone: 'healthy', vacancyCount: 1, totalCupos: 2 } }],
    }],
  }],
};

test('escapeHtml encodes text and attribute breaking characters', () => {
  assert.equal(escapeHtml(`<>&"'`), '&lt;&gt;&amp;&quot;&#39;');
});

test('login renderer follows canonical accessible copy and never repopulates password', () => {
  const html = renderLoginPage({ csrfToken: `token"><script>bad()</script>`, error: 'Usuario o contraseña incorrectos. Volvé a intentarlo.', cspNonce: 'nonce-1' });

  assert.match(html, /Dashboard de UADE Bot/);
  assert.match(html, /Ingresá con las credenciales configuradas por el operador\./);
  assert.match(html, /<label[^>]*for="username"[^>]*>Usuario<\/label>/);
  assert.match(html, /name="username"[^>]*autocomplete="username"[^>]*autofocus/);
  assert.match(html, /name="password"[^>]*autocomplete="current-password"/);
  assert.match(html, /aria-describedby="login-help login-error"/);
  assert.doesNotMatch(html, /type="password"[^>]*value=/);
  assert.doesNotMatch(html, /<script>bad\(\)<\/script>/);
  assert.match(html, /<style nonce="nonce-1">/);
});

test('dashboard SSR renders semantic, keyed, escaped operational content and exact design tokens', () => {
  const html = renderDashboardPage({ snapshot, csrfToken: 'csrf-1', cspNonce: 'nonce-2' });

  assert.equal((html.match(/<h1\b/g) ?? []).length, 1);
  assert.match(html, /<header\b/);
  assert.match(html, /<main\b/);
  assert.match(html, /Estado del sistema/);
  assert.equal((html.match(/class="health-card"/g) ?? []).length, 4);
  assert.match(html, /<details[^>]*data-account-id="user-1"[^>]*open/);
  assert.match(html, /data-job-id="7"/);
  assert.match(html, /<caption>Historial de cambios<\/caption>/);
  assert.match(html, /<th scope="col">Fecha<\/th>/);
  assert.match(html, /Operador &lt;script&gt;alert\(1\)&lt;\/script&gt;/);
  assert.doesNotMatch(html, /Operador <script>/);
  assert.match(html, /--space-xs: 4px/);
  assert.match(html, /--color-accent: #2563EB/);
  assert.match(html, /font-family: system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif/);
  assert.match(html, /@media \(min-width: 640px\)/);
  assert.match(html, /@media \(min-width: 1024px\)/);
  assert.match(html, /@media \(prefers-reduced-motion: reduce\)/);
  assert.match(html, /outline: 2px solid var\(--color-accent\)/);
  assert.match(html, /min-height: 44px/);
  assert.match(html, /<style nonce="nonce-2">/);
  assert.match(html, /<script nonce="nonce-2">/);
  assert.doesNotMatch(html, /lastOutcome|uadeStartUrl|ciphertext|param=/i);
});

test('dashboard SSR has the canonical empty state and a fixed safe error page', () => {
  const html = renderDashboardPage({ snapshot: { ...snapshot, accounts: [], health: { ...snapshot.health, jobs: { total: 0, active: 0, manuallyPaused: 0 } } }, csrfToken: 'csrf', cspNonce: 'nonce' });
  assert.match(html, /No hay búsquedas registradas/);
  assert.match(html, /Cuando se cree una búsqueda desde Discord, aparecerá acá automáticamente\./);

  const error = renderSafeError({ cspNonce: 'safe', status: 500 });
  assert.match(error, /No pudimos mostrar el dashboard/);
  assert.doesNotMatch(error, /stack|Error:|SQL|param=/i);
});

function refreshHarness() {
  const timers = [];
  const events = [];
  let now = 1_000;
  let resolveFetch;
  let fetchCount = 0;
  const controller = createRefreshController({
    fetchSnapshot: () => {
      fetchCount += 1;
      return new Promise((resolve) => { resolveFetch = resolve; });
    },
    schedule: (callback, delay) => { timers.push({ callback, delay }); return timers.length; },
    cancel: () => {},
    now: () => now,
    onBusy: (busy) => events.push(['busy', busy]),
    onSuccess: (value) => events.push(['success', value]),
    onFailure: () => events.push(['failure']),
    onUnauthorized: () => events.push(['unauthorized']),
  });
  return {
    controller, timers, events,
    get fetchCount() { return fetchCount; },
    setNow(value) { now = value; },
    resolve(value) { resolveFetch(value); },
  };
}

test('refresh schedules 10 seconds from completion and never overlaps', async () => {
  const harness = refreshHarness();
  harness.controller.start();
  assert.equal(harness.timers.shift().delay, 10_000);
  const first = harness.controller.refresh();
  assert.equal(harness.fetchCount, 1);
  assert.equal(harness.controller.refresh(), first);
  assert.equal(harness.fetchCount, 1);
  harness.resolve({ ok: true, status: 200, snapshot: { generatedAt: 2_000 } });
  await first;
  assert.equal(harness.timers.at(-1).delay, 10_000);
  assert.deepEqual(harness.events.slice(-2), [['success', { generatedAt: 2_000 }], ['busy', false]]);
});

test('refresh retries after failures, visibility recovery waits over 10s, and 401 is terminal', async () => {
  const harness = refreshHarness();
  harness.controller.start();
  const failed = harness.controller.refresh();
  harness.resolve({ ok: false, status: 503 });
  await failed;
  assert.ok(harness.events.some(([event]) => event === 'failure'));
  assert.equal(harness.timers.at(-1).delay, 10_000);

  harness.controller.hidden();
  harness.setNow(10_999);
  assert.equal(harness.controller.visible(), null);
  harness.setNow(11_001);
  const recovery = harness.controller.visible();
  assert.equal(harness.fetchCount, 2);
  harness.resolve({ ok: false, status: 401 });
  await recovery;
  assert.ok(harness.events.some(([event]) => event === 'unauthorized'));
  assert.equal(harness.controller.stopped, true);
  assert.equal(harness.controller.retry(), null);
});

test('browser script patches stable keys without replacing the dashboard root or using storage/cookies', () => {
  const html = renderDashboardPage({ snapshot, csrfToken: 'csrf', cspNonce: 'nonce' });
  const script = html.match(/<script nonce="nonce">([\s\S]*?)<\/script>/)?.[1] ?? '';

  assert.match(script, /fetch\('\/api\/dashboard'/);
  assert.match(script, /credentials: 'same-origin'/);
  assert.match(script, /cache: 'no-store'/);
  assert.match(script, /data-account-id/);
  assert.match(script, /data-job-id/);
  assert.match(script, /textContent/);
  assert.match(script, /visibilitychange/);
  assert.match(script, /Tu sesión venció\. Iniciá sesión de nuevo\./);
  assert.doesNotMatch(script, /localStorage|sessionStorage|document\.cookie|\.innerHTML\s*=/);
});
