import assert from 'node:assert/strict';
import { test } from 'node:test';

import express from 'express';

import { createDashboardAuth } from './auth.js';
import { registerDashboardRoutes } from './routes.js';
import { createDashboardApp, startDashboardServer } from './server.js';

const env = {
  DASHBOARD_USERNAME: 'operator',
  DASHBOARD_PASSWORD: 'correct horse',
  DASHBOARD_SESSION_SECRET: 's'.repeat(32),
};

function createRouteHarness({ snapshotBuilder = () => ({ health: { jobs: { total: 7 } }, accounts: [] }), loginLimit = 5 } = {}) {
  const app = express();
  app.disable('x-powered-by');
  app.use(express.urlencoded({ extended: false, limit: '4kb' }));
  const auth = createDashboardAuth({ env });
  app.use(auth.sessionMiddleware);
  registerDashboardRoutes({
    app,
    auth,
    db: { marker: 'db' },
    client: { marker: 'client' },
    snapshotBuilder,
    loginLimit,
    renderLogin: ({ csrfToken, error }) => JSON.stringify({ csrfToken, error }),
    renderDashboard: ({ snapshot, csrfToken }) => JSON.stringify({ snapshot, csrfToken }),
  });
  app.use((_error, _req, res, _next) => res.status(500).json({ error: 'internal_error' }));
  return app;
}

async function listen(app) {
  const server = await new Promise((resolve) => {
    const value = app.listen(0, '127.0.0.1', () => resolve(value));
  });
  return { server, baseUrl: `http://127.0.0.1:${server.address().port}` };
}

function cookieFrom(response) {
  return response.headers.getSetCookie().map((value) => value.split(';', 1)[0]).join('; ');
}

async function getLogin(baseUrl) {
  const response = await fetch(`${baseUrl}/login`, { redirect: 'manual' });
  return { response, cookie: cookieFrom(response), csrfToken: JSON.parse(await response.text()).csrfToken };
}

async function login(baseUrl, cookie, csrfToken, password = env.DASHBOARD_PASSWORD) {
  return fetch(`${baseUrl}/login`, {
    method: 'POST',
    redirect: 'manual',
    headers: { cookie, 'content-type': 'application/x-www-form-urlencoded', origin: baseUrl },
    body: new URLSearchParams({ username: env.DASHBOARD_USERNAME, password, _csrf: csrfToken }),
  });
}

test('protected routes deny before building data and set no-store', async (t) => {
  let builds = 0;
  const { server, baseUrl } = await listen(createRouteHarness({ snapshotBuilder: () => { builds += 1; return { sentinel: 'OPERATIVE_SECRET' }; } }));
  t.after(() => server.close());

  const api = await fetch(`${baseUrl}/api/dashboard`, { redirect: 'manual' });
  assert.equal(api.status, 401);
  assert.equal(api.headers.get('cache-control'), 'no-store');
  assert.doesNotMatch(await api.text(), /OPERATIVE_SECRET|health|accounts/);
  const html = await fetch(`${baseUrl}/dashboard`, { redirect: 'manual', headers: { accept: 'text/html' } });
  assert.equal(html.status, 303);
  assert.equal(builds, 0);
});

test('login is generic, session unlocks shared snapshot, logout requires CSRF and revokes', async (t) => {
  const calls = [];
  const snapshot = { health: { jobs: { total: 1 } }, accounts: [{ discordUserId: '1' }] };
  const { server, baseUrl } = await listen(createRouteHarness({ snapshotBuilder: (deps) => { calls.push(deps); return snapshot; } }));
  t.after(() => server.close());

  const challenge = await getLogin(baseUrl);
  const failed = await login(baseUrl, challenge.cookie, challenge.csrfToken, 'wrong');
  assert.equal(failed.status, 401);
  assert.match(await failed.text(), /Usuario o contraseña incorrectos/);
  assert.doesNotMatch(await (await login(baseUrl, challenge.cookie, challenge.csrfToken, 'also-wrong')).text(), /operator|wrong|also-wrong/);

  const successful = await login(baseUrl, challenge.cookie, challenge.csrfToken);
  assert.equal(successful.status, 303);
  const sessionCookie = cookieFrom(successful);
  const dashboard = await fetch(`${baseUrl}/dashboard`, { headers: { cookie: sessionCookie } });
  const dashboardBody = JSON.parse(await dashboard.text());
  const api = await fetch(`${baseUrl}/api/dashboard`, { headers: { cookie: sessionCookie } });
  assert.deepEqual((await api.json()), snapshot);
  assert.deepEqual(dashboardBody.snapshot, snapshot);
  assert.equal(calls.length, 2);
  assert.equal(calls[0].db.marker, 'db');

  const rejectedLogout = await fetch(`${baseUrl}/logout`, { method: 'POST', headers: { cookie: sessionCookie, origin: baseUrl } });
  assert.equal(rejectedLogout.status, 403);
  const logout = await fetch(`${baseUrl}/logout`, {
    method: 'POST', redirect: 'manual',
    headers: { cookie: sessionCookie, 'content-type': 'application/x-www-form-urlencoded', origin: baseUrl },
    body: new URLSearchParams({ _csrf: dashboardBody.csrfToken }),
  });
  assert.equal(logout.status, 303);
  assert.equal((await fetch(`${baseUrl}/api/dashboard`, { headers: { cookie: sessionCookie } })).status, 401);
});

test('login limiter is scoped to POST login and errors remain generic', async (t) => {
  const { server, baseUrl } = await listen(createRouteHarness({ loginLimit: 1 }));
  t.after(() => server.close());
  const challenge = await getLogin(baseUrl);
  assert.equal((await login(baseUrl, challenge.cookie, challenge.csrfToken, 'wrong')).status, 401);
  const limited = await login(baseUrl, challenge.cookie, challenge.csrfToken, 'wrong');
  assert.equal(limited.status, 429);
  assert.doesNotMatch(await limited.text(), /operator|correct horse|stack|Error:/);
  assert.equal((await fetch(`${baseUrl}/login`)).status, 200);
});

test('snapshot failures return a generic 500 without internal details', async (t) => {
  const { server, baseUrl } = await listen(createRouteHarness({ snapshotBuilder: () => { throw new Error('SQL SENTINEL param=secret'); } }));
  t.after(() => server.close());
  const challenge = await getLogin(baseUrl);
  const successful = await login(baseUrl, challenge.cookie, challenge.csrfToken);
  const response = await fetch(`${baseUrl}/api/dashboard`, { headers: { cookie: cookieFrom(successful) } });
  assert.equal(response.status, 500);
  assert.equal(response.headers.get('cache-control'), 'no-store');
  assert.doesNotMatch(await response.text(), /SQL|SENTINEL|param=|stack/);
});

test('server module is side-effect free and app factory emits nonce security headers', async (t) => {
  const warnings = [];
  const app = createDashboardApp({
    db: {}, client: {},
    env: { ...env, DASHBOARD_USERNAME: 'admin', DASHBOARD_PASSWORD: 'admin', DASHBOARD_SESSION_SECRET: undefined },
    logger: { info() {}, error() {}, warn(fields) { warnings.push(fields); } },
    randomBytes: (size) => Buffer.alloc(size, 9),
    snapshotBuilder: () => ({ health: {}, accounts: [] }),
    renderLogin: ({ csrfToken, cspNonce }) => `<form><input name="_csrf" value="${csrfToken}"><script nonce="${cspNonce}"></script></form>`,
  });
  assert.equal(app.get('trust proxy'), false);
  const { server, baseUrl } = await listen(app);
  t.after(() => server.close());
  const response = await fetch(`${baseUrl}/login`);
  const body = await response.text();
  const nonce = body.match(/nonce="([^"]+)"/)?.[1];

  assert.ok(nonce);
  assert.match(response.headers.get('content-security-policy'), new RegExp(`'nonce-${nonce}'`));
  assert.equal(response.headers.get('x-powered-by'), null);
  assert.equal(response.headers.get('x-content-type-options'), 'nosniff');
  assert.equal(response.headers.get('x-frame-options'), 'SAMEORIGIN');
  assert.equal(response.headers.get('referrer-policy'), 'no-referrer');
  assert.deepEqual(warnings.map((entry) => entry.event).sort(), ['dashboard_default_credentials', 'dashboard_ephemeral_session_secret']);
  assert.doesNotMatch(JSON.stringify(warnings), /admin\/admin|ssss|secret|password/i);
});

test('startDashboardServer binds explicitly and closes cleanly while reusing dependencies', async () => {
  const db = { identity: 'shared-db' };
  const client = { identity: 'shared-client' };
  const server = await startDashboardServer({
    db, client,
    env: { ...env, DASHBOARD_PORT: 0 },
    logger: { info() {}, error() {}, warn() {} },
    snapshotBuilder: ({ db: receivedDb, client: receivedClient }) => {
      assert.equal(receivedDb, db);
      assert.equal(receivedClient, client);
      return { health: {}, accounts: [] };
    },
  });
  assert.equal(server.address().address, '0.0.0.0');
  await new Promise((resolve, reject) => server.close((error) => error ? reject(error) : resolve()));
  assert.equal(server.listening, false);
});
