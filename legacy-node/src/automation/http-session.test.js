import assert from 'node:assert/strict';
import http from 'node:http';
import test from 'node:test';

import { withHttpSession } from './http-session.js';

async function listen(t, handler) {
  const server = http.createServer(handler);
  await new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', resolve);
  });
  t.after(() => new Promise((resolve) => {
    server.closeAllConnections();
    server.close(resolve);
  }));
  const { port } = server.address();
  return `http://127.0.0.1:${port}`;
}

test('sends scoped Basic auth, follows same-origin redirects, and persists cookies', async (t) => {
  const expectedAuth = `Basic ${Buffer.from('dummy-user:dummy-pass').toString('base64')}`;
  const seen = [];
  const origin = await listen(t, (req, res) => {
    seen.push({ path: req.url, authorization: req.headers.authorization, cookie: req.headers.cookie });
    if (req.url === '/start') {
      res.writeHead(302, { location: '/final', 'set-cookie': 'session=isolated; Path=/; HttpOnly' });
      res.end();
      return;
    }
    res.writeHead(200, { 'content-type': 'text/plain' });
    res.end('ok');
  });

  const result = await withHttpSession(
    { username: 'dummy-user', password: 'dummy-pass' },
    (session) => session.request(`${origin}/start`),
    { allowedOrigin: origin },
  );

  assert.equal(result.status, 200);
  assert.equal(result.body, 'ok');
  assert.deepEqual(seen, [
    { path: '/start', authorization: expectedAuth, cookie: undefined },
    { path: '/final', authorization: expectedAuth, cookie: 'session=isolated' },
  ]);
});

test('parallel sessions never share cookies or Authorization', async (t) => {
  const observations = [];
  const origin = await listen(t, (req, res) => {
    observations.push({ authorization: req.headers.authorization, cookie: req.headers.cookie });
    if (req.url === '/seed-a') {
      res.writeHead(200, { 'set-cookie': 'owner=a; Path=/' });
    } else if (req.url === '/seed-b') {
      res.writeHead(200, { 'set-cookie': 'owner=b; Path=/' });
    } else {
      res.writeHead(200);
    }
    res.end('ok');
  });

  await Promise.all([
    withHttpSession({ username: 'user-a', password: 'pass-a' }, async (session) => {
      await session.request(`${origin}/seed-a`);
      await session.request(`${origin}/check`);
    }, { allowedOrigin: origin }),
    withHttpSession({ username: 'user-b', password: 'pass-b' }, async (session) => {
      await session.request(`${origin}/seed-b`);
      await session.request(`${origin}/check`);
    }, { allowedOrigin: origin }),
  ]);

  const aAuth = `Basic ${Buffer.from('user-a:pass-a').toString('base64')}`;
  const bAuth = `Basic ${Buffer.from('user-b:pass-b').toString('base64')}`;
  assert.equal(observations.filter((entry) => entry.authorization === aAuth).length, 2);
  assert.equal(observations.filter((entry) => entry.authorization === bAuth).length, 2);
  assert.deepEqual(
    observations.filter((entry) => entry.cookie).map((entry) => [entry.authorization, entry.cookie]).sort(),
    [[aAuth, 'owner=a'], [bAuth, 'owner=b']].sort(),
  );
});

test('rejects cross-origin redirects before forwarding credentials', async (t) => {
  let targetRequests = 0;
  const targetOrigin = await listen(t, (_req, res) => {
    targetRequests += 1;
    res.end('must not be reached');
  });
  const origin = await listen(t, (_req, res) => {
    res.writeHead(302, { location: `${targetOrigin}/leak` });
    res.end();
  });

  await assert.rejects(
    withHttpSession(
      { username: 'dummy-user', password: 'dummy-pass' },
      (session) => session.request(`${origin}/start`),
      { allowedOrigin: origin },
    ),
    { code: 'HTTP_REDIRECT_CROSS_ORIGIN', message: 'HTTP_REDIRECT_CROSS_ORIGIN' },
  );
  assert.equal(targetRequests, 0);
});

test('rejects redirect downgrade, loops, and redirect exhaustion with fixed reasons', async (t) => {
  const downgradeFetch = async () => new Response(null, {
    status: 302,
    headers: { location: 'http://inscripcionespia.uade.edu.ar/insecure' },
  });
  await assert.rejects(
    withHttpSession(
      { username: 'dummy-user', password: 'dummy-pass' },
      (session) => session.request('https://inscripcionespia.uade.edu.ar/start'),
      { fetchImpl: downgradeFetch },
    ),
    { code: 'HTTP_REDIRECT_DOWNGRADE' },
  );

  const origin = await listen(t, (req, res) => {
    const next = req.url === '/a' ? '/b' : '/a';
    res.writeHead(302, { location: next });
    res.end();
  });
  await assert.rejects(
    withHttpSession(
      { username: 'dummy-user', password: 'dummy-pass' },
      (session) => session.request(`${origin}/a`),
      { allowedOrigin: origin },
    ),
    { code: 'HTTP_REDIRECT_LOOP' },
  );

  await assert.rejects(
    withHttpSession(
      { username: 'dummy-user', password: 'dummy-pass' },
      (session) => session.request(`${origin}/a`),
      { allowedOrigin: origin, maxRedirects: 0 },
    ),
    { code: 'HTTP_TOO_MANY_REDIRECTS' },
  );
});

test('aborts timed-out and oversized local responses with sanitized fixed reasons', async (t) => {
  const origin = await listen(t, (req, res) => {
    if (req.url === '/slow') {
      setTimeout(() => {
        res.writeHead(200);
        res.end('late');
      }, 100);
      return;
    }
    res.writeHead(200, { 'content-type': 'text/plain' });
    res.write('12345');
    res.end('67890');
  });

  await assert.rejects(
    withHttpSession(
      { username: 'dummy-user', password: 'dummy-pass' },
      (session) => session.request(`${origin}/slow`, { timeoutMs: 10 }),
      { allowedOrigin: origin },
    ),
    { code: 'HTTP_TIMEOUT', message: 'HTTP_TIMEOUT' },
  );

  await assert.rejects(
    withHttpSession(
      { username: 'dummy-user', password: 'dummy-pass' },
      (session) => session.request(`${origin}/large`, { maxBodyBytes: 5 }),
      { allowedOrigin: origin },
    ),
    { code: 'HTTP_BODY_TOO_LARGE', message: 'HTTP_BODY_TOO_LARGE' },
  );
});

test('rejects non-allowlisted initial URLs and normalizes native transport errors', async () => {
  let fetchCalls = 0;
  await assert.rejects(
    withHttpSession(
      { username: 'dummy-user', password: 'dummy-pass' },
      (session) => session.request('https://example.invalid/path'),
      { fetchImpl: async () => { fetchCalls += 1; } },
    ),
    { code: 'HTTP_ORIGIN_NOT_ALLOWED' },
  );
  assert.equal(fetchCalls, 0);

  await assert.rejects(
    withHttpSession(
      { username: 'dummy-user', password: 'dummy-pass' },
      (session) => session.request('https://inscripcionespia.uade.edu.ar/path'),
      { fetchImpl: async () => { throw new Error('native details must not escape'); } },
    ),
    { code: 'HTTP_TRANSPORT_ERROR', message: 'HTTP_TRANSPORT_ERROR' },
  );
});
