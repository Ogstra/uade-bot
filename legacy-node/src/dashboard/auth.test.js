import assert from 'node:assert/strict';
import { test } from 'node:test';

import {
  createDashboardAuth,
  createSessionRegistry,
  verifyDashboardCredentials,
} from './auth.js';

function deterministicRandom() {
  let value = 0;
  return (size) => Buffer.alloc(size, ++value);
}

test('credential verification is generic and performs two fixed-length comparisons', () => {
  const lengths = [];
  const compare = (left, right) => {
    lengths.push([left.length, right.length]);
    return Buffer.compare(left, right) === 0;
  };

  assert.equal(verifyDashboardCredentials({ username: 'admin', password: 'wrong' }, { username: 'admin', password: 'secret' }, compare), false);
  assert.deepEqual(lengths, [[32, 32], [32, 32]]);
  lengths.length = 0;
  assert.equal(verifyDashboardCredentials({ username: 'nobody', password: 'secret' }, { username: 'admin', password: 'secret' }, compare), false);
  assert.deepEqual(lengths, [[32, 32], [32, 32]]);
});

test('sessions rotate, expire, revoke and keep only opaque security state', () => {
  let now = 1_000;
  const registry = createSessionRegistry({ clock: () => now, randomBytes: deterministicRandom(), maxEntries: 2 });
  const first = registry.createAuthenticatedSession();
  const rotated = registry.createAuthenticatedSession(first.sid);

  assert.notEqual(first.sid, rotated.sid);
  assert.equal(registry.getAuthenticated(first.sid), null);
  assert.deepEqual(Object.keys(registry.getAuthenticated(rotated.sid)).sort(), ['csrfToken', 'expiresAt']);
  assert.doesNotMatch(JSON.stringify(registry.debugSnapshot()), /admin|password|secret|snapshot|UADE/i);

  registry.revoke(rotated.sid);
  assert.equal(registry.getAuthenticated(rotated.sid), null);

  const expiring = registry.createAuthenticatedSession();
  now += 8 * 60 * 60 * 1_000 + 1;
  assert.equal(registry.getAuthenticated(expiring.sid), null);

  registry.createLoginChallenge();
  registry.createLoginChallenge();
  registry.createLoginChallenge();
  assert.ok(registry.size <= 2);
});

test('cookie configuration is secure only for production HTTPS semantics', () => {
  const common = { DASHBOARD_USERNAME: 'operator', DASHBOARD_PASSWORD: 'secret', DASHBOARD_SESSION_SECRET: 'x'.repeat(32) };
  const development = createDashboardAuth({ env: { ...common, NODE_ENV: 'development' } });
  const production = createDashboardAuth({ env: { ...common, NODE_ENV: 'production' } });

  assert.equal(development.cookieOptions.name, 'uade_dashboard');
  assert.equal(development.cookieOptions.secure, false);
  assert.equal(production.cookieOptions.name, '__Host-uade_dashboard');
  assert.equal(production.cookieOptions.secure, true);
  assert.equal(production.cookieOptions.httpOnly, true);
  assert.equal(production.cookieOptions.sameSite, 'lax');
});

test('CSRF rejects missing token and cross-site metadata before mutation', () => {
  const auth = createDashboardAuth({
    env: { DASHBOARD_USERNAME: 'admin', DASHBOARD_PASSWORD: 'admin', DASHBOARD_SESSION_SECRET: 'x'.repeat(32) },
    randomBytes: deterministicRandom(),
  });
  const session = auth.registry.createAuthenticatedSession();
  let mutated = false;
  const response = { statusCode: 200, status(code) { this.statusCode = code; return this; }, send() { return this; } };

  auth.requireMutationCsrf({ session: { sid: session.sid }, body: {}, headers: {}, get() { return undefined; } }, response, () => { mutated = true; });
  assert.equal(response.statusCode, 403);
  assert.equal(mutated, false);

  response.statusCode = 200;
  auth.requireMutationCsrf({
    session: { sid: session.sid },
    body: { _csrf: session.csrfToken },
    headers: { 'sec-fetch-site': 'cross-site' },
    get(name) { return this.headers[name.toLowerCase()]; },
  }, response, () => { mutated = true; });
  assert.equal(response.statusCode, 403);
  assert.equal(mutated, false);
});
