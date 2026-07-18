import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import http from 'node:http';
import test from 'node:test';

import { withHttpSession } from './http-session.js';
import { runHttpSearch } from './http-search.js';

const FIXTURE_ROOT = new URL('./__fixtures__/webforms/', import.meta.url);
const [initialForm, postbackFound, postbackEmpty, postbackMismatch, malformedDelta] = await Promise.all([
  readFile(new URL('initial-form.html', FIXTURE_ROOT), 'utf8'),
  readFile(new URL('postback-found.html', FIXTURE_ROOT), 'utf8'),
  readFile(new URL('postback-empty.html', FIXTURE_ROOT), 'utf8'),
  readFile(new URL('postback-mismatch.html', FIXTURE_ROOT), 'utf8'),
  readFile(new URL('delta-malformed.txt', FIXTURE_ROOT), 'utf8'),
]);

const filtros = Object.freeze({
  materiaCodigo: '3.1.050',
  ofrecimiento: 'curricular',
  turno: 'mañana',
  dias: ['LU', 'MI'],
  sedesExcluidas: [],
});
const credentials = Object.freeze({ username: 'dummy-user', password: 'dummy-pass' });
const expectedAuth = `Basic ${Buffer.from('dummy-user:dummy-pass').toString('base64')}`;

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
  return `http://127.0.0.1:${server.address().port}`;
}

async function createWebFormsServer(t, scenario = {}) {
  const requests = [];
  const protocolErrors = [];
  const origin = await listen(t, async (req, res) => {
    const chunks = [];
    for await (const chunk of req) chunks.push(chunk);
    const body = Buffer.concat(chunks).toString('utf8');
    requests.push({ method: req.method, path: req.url, headers: req.headers, body });

    if (req.headers.authorization !== expectedAuth) {
      protocolErrors.push('unexpected Authorization');
    }

    if (req.url?.startsWith('/start')) {
      if (scenario.getStatus) {
        res.writeHead(scenario.getStatus);
        res.end();
        return;
      }
      if (scenario.invalidRedirect) {
        res.writeHead(302, { location: 'http://127.0.0.1:1/not-allowed' });
        res.end();
        return;
      }
      res.writeHead(302, {
        location: '/InscripcionClaseBuscar.aspx',
        'set-cookie': 'ASP.NET_SessionId=dummy-session; Path=/; HttpOnly',
      });
      res.end();
      return;
    }

    if (req.method === 'GET') {
      if (req.headers.cookie !== 'ASP.NET_SessionId=dummy-session') {
        protocolErrors.push('GET cookie jar mismatch');
      }
      res.writeHead(200, { 'content-type': 'text/html; charset=utf-8' });
      res.end(scenario.initialHtml ?? initialForm);
      return;
    }

    if (req.method !== 'POST') {
      protocolErrors.push('unexpected method');
    }
    if (req.headers.cookie !== 'ASP.NET_SessionId=dummy-session') {
      protocolErrors.push('POST cookie jar mismatch');
    }
    if (req.headers.origin !== origin) {
      protocolErrors.push('Origin mismatch');
    }
    if (req.headers.referer !== `${origin}/InscripcionClaseBuscar.aspx`) {
      protocolErrors.push('Referer mismatch');
    }
    if (req.headers['content-type'] !== 'application/x-www-form-urlencoded') {
      protocolErrors.push('content type mismatch');
    }
    const form = new URLSearchParams(body);
    const expectedFields = {
      __VIEWSTATE: 'DUMMY_VIEWSTATE',
      __EVENTVALIDATION: 'DUMMY_EVENTVALIDATION',
      'ctl00$ContentPlaceHolder1$optOfrecimiento': '145',
      'ctl00$ContentPlaceHolder1$cboTurno': '10152',
      'ctl00$ContentPlaceHolder1$chkLunes': 'on',
      'ctl00$ContentPlaceHolder1$chkMiercoles': 'on',
      'ctl00$ContentPlaceHolder1$btnBuscar': 'Buscar',
    };
    for (const [name, value] of Object.entries(expectedFields)) {
      if (form.get(name) !== value) protocolErrors.push(`body mismatch: ${name}`);
    }
    if (form.has('disabledField') || form.has('uncheckedField')) {
      protocolErrors.push('unsuccessful controls serialized');
    }

    if (scenario.postStatus) {
      res.writeHead(scenario.postStatus);
      res.end();
      return;
    }
    if (scenario.slowPost) {
      setTimeout(() => {
        res.writeHead(200, { 'content-type': 'text/html' });
        res.end(postbackFound);
      }, 100);
      return;
    }
    res.writeHead(200, { 'content-type': scenario.contentType ?? 'text/html; charset=utf-8' });
    res.end(scenario.postBody ?? postbackFound);
  });

  return { origin, startUrl: `${origin}/start?param=dummy`, requests, protocolErrors };
}

async function searchAgainst(t, scenario = {}, limits) {
  const harness = await createWebFormsServer(t, scenario);
  const result = await withHttpSession(
    credentials,
    (session) => runHttpSearch(session, filtros, { startUrl: harness.startUrl, limits }),
    { allowedOrigin: harness.origin },
  );
  return { ...harness, result };
}

test('validates filtros before any I/O', async () => {
  let requests = 0;
  await assert.rejects(
    runHttpSearch({ request: async () => { requests += 1; } }, { ...filtros, materiaCodigo: 'invalid' }, {
      startUrl: 'http://127.0.0.1:1/start',
    }),
  );
  assert.equal(requests, 0);
});

test('performs strict redirected GET and full WebForms POST before returning verified found HTML', async (t) => {
  const { result, requests, protocolErrors } = await searchAgainst(t);
  assert.deepEqual(protocolErrors, []);
  assert.deepEqual(requests.map(({ method, path }) => [method, path]), [
    ['GET', '/start?param=dummy'],
    ['GET', '/InscripcionClaseBuscar.aspx'],
    ['POST', '/InscripcionClaseBuscar.aspx'],
  ]);
  assert.equal(result.status, 'verified');
  assert.equal(result.html, postbackFound);
  assert.equal(result.materiaNombre, 'Física II');
});

test('returns verified for a positively reflected empty result', async (t) => {
  const { result, protocolErrors } = await searchAgainst(t, { postBody: postbackEmpty });
  assert.deepEqual(protocolErrors, []);
  assert.equal(result.status, 'verified');
  assert.equal(result.html, postbackEmpty);
});

test('maps GET and POST status gates to the inherited public outcomes', async (t) => {
  const unauthorized = await searchAgainst(t, { getStatus: 401 });
  assert.deepEqual(unauthorized.result, { status: 'invalid_credentials' });
  const limited = await searchAgainst(t, { getStatus: 429 });
  assert.deepEqual(limited.result, { status: 'rate_limited' });
  const postUnauthorized = await searchAgainst(t, { postStatus: 401 });
  assert.deepEqual(postUnauthorized.result, { status: 'invalid_credentials' });
  const postLimited = await searchAgainst(t, { postStatus: 429 });
  assert.deepEqual(postLimited.result, { status: 'rate_limited' });
});

test('detects an empty turno catalog as stale_start_url', async (t) => {
  const staleHtml = initialForm.replace(/(<select id="ContentPlaceHolder1_cboTurno"[^>]*>)[\s\S]*?(<\/select>)/, '$1$2');
  const { result } = await searchAgainst(t, { initialHtml: staleHtml });
  assert.deepEqual(result, { status: 'stale_start_url' });
});

test('fails closed for reflected mismatch and invalid HTML', async (t) => {
  const mismatch = await searchAgainst(t, { postBody: postbackMismatch });
  assert.deepEqual(mismatch.result, { status: 'search_failed', reason: 'postback_mismatch' });
  const invalid = await searchAgainst(t, { postBody: '<html><body>not a reflected form</body></html>' });
  assert.deepEqual(invalid.result, { status: 'search_failed', reason: 'postback_mismatch' });
});

test('parses delta responses but verifies only positively reflected panel HTML', async (t) => {
  const validDelta = `${postbackFound.length}|updatePanel|ctl00$UpdatePanelContenido|${postbackFound}|`;
  const valid = await searchAgainst(t, { postBody: validDelta, contentType: 'text/plain; charset=utf-8' });
  assert.equal(valid.result.status, 'verified');
  assert.equal(valid.result.html, postbackFound);

  const malformed = await searchAgainst(t, { postBody: malformedDelta, contentType: 'text/plain' });
  assert.deepEqual(malformed.result, { status: 'search_failed', reason: 'delta_malformed' });
});

test('normalizes redirect, timeout, and response cap failures without native details', async (t) => {
  const redirect = await searchAgainst(t, { invalidRedirect: true });
  assert.deepEqual(redirect.result, { status: 'search_failed', reason: 'redirect_invalid' });

  const timeout = await searchAgainst(t, { slowPost: true }, { timeoutMs: 10 });
  assert.deepEqual(timeout.result, { status: 'search_failed', reason: 'transport_timeout' });

  const oversized = await searchAgainst(t, { postBody: postbackFound }, { maxBodyBytes: Buffer.byteLength(initialForm) + 10 });
  assert.deepEqual(oversized.result, { status: 'search_failed', reason: 'response_too_large' });
});
