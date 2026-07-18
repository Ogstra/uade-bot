import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { readFile } from 'node:fs/promises';
import http from 'node:http';
import { createRequire } from 'node:module';
import { performance } from 'node:perf_hooks';

const RSS_LIMIT_BYTES = 10 * 1024 * 1024;
const FIXTURE_ROOT = new URL('../src/automation/__fixtures__/webforms/', import.meta.url);
const EVIDENCE_ROOT = new URL('../.planning/phases/03.2-motor-http-sin-navegador/evidence/webforms-capture-sanitized/', import.meta.url);
const FILTERS = Object.freeze({
  materiaCodigo: '3.1.050',
  ofrecimiento: 'curricular',
  turno: 'mañana',
  dias: ['LU', 'MI'],
  sedesExcluidas: [],
});
const DUMMY_CREDENTIALS = Object.freeze({ username: 'benchmark-user', password: 'benchmark-password' });

function requireNode24() {
  const major = Number.parseInt(process.versions.node.split('.')[0], 10);
  assert.equal(major, 24, `benchmark requires Node 24; received ${process.versions.node}`);
  assert.equal(typeof global.gc, 'function', 'benchmark requires --expose-gc');
}

function positiveInteger(value, name) {
  const parsed = Number.parseInt(value, 10);
  assert(Number.isSafeInteger(parsed) && parsed > 0, `${name} must be a positive integer`);
  return parsed;
}

function parseArgs(argv) {
  const values = new Map();
  let worker = false;
  for (let index = 0; index < argv.length; index += 1) {
    if (argv[index] === '--worker') {
      worker = true;
      continue;
    }
    const name = argv[index];
    const value = argv[index + 1];
    assert(name?.startsWith('--') && value && !value.startsWith('--'), `invalid argument ${name ?? ''}`);
    values.set(name.slice(2), value);
    index += 1;
  }

  const scenario = values.get('scenario');
  assert(['isolated', 'concurrent'].includes(scenario), 'scenario must be isolated or concurrent');
  return {
    worker,
    scenario,
    runs: values.has('runs') ? positiveInteger(values.get('runs'), 'runs') : 1,
    concurrency: values.has('concurrency') ? positiveInteger(values.get('concurrency'), 'concurrency') : 1,
  };
}

function padToApprovedBytes(html, approvedBytes, label) {
  const currentBytes = Buffer.byteLength(html);
  assert(currentBytes <= approvedBytes, `${label} exceeds its approved captured size`);
  return html + ' '.repeat(approvedBytes - currentBytes);
}

async function loadFixtures() {
  const [manifestText, initialTemplate, postbackTemplate] = await Promise.all([
    readFile(new URL('manifest.json', EVIDENCE_ROOT), 'utf8'),
    readFile(new URL('initial-form.html', FIXTURE_ROOT), 'utf8'),
    readFile(new URL('postback-found.html', FIXTURE_ROOT), 'utf8'),
  ]);
  const manifest = JSON.parse(manifestText);
  assert.equal(manifest.postbackModeAccepted, 'accepted');
  const initialApprovedBytes = positiveInteger(manifest.responses?.initialGet?.bytes, 'initial fixture bytes');
  const postbackApprovedBytes = positiveInteger(manifest.responses?.fullPostback?.bytes, 'postback fixture bytes');
  const maxBodyBytes = positiveInteger(manifest.derivedLimits?.maxBodyBytes, 'max body bytes');
  return {
    initial: padToApprovedBytes(initialTemplate, initialApprovedBytes, 'initial fixture'),
    postback: padToApprovedBytes(postbackTemplate, postbackApprovedBytes, 'postback fixture'),
    bytes: {
      initialGet: initialApprovedBytes,
      fullPostback: postbackApprovedBytes,
      asyncPostback: positiveInteger(manifest.responses?.asyncPostback?.bytes, 'async fixture bytes'),
      measuredMaxBody: positiveInteger(manifest.derivedLimits?.measuredMaxBodyBytes, 'measured max body bytes'),
      maxBody: maxBodyBytes,
    },
  };
}

async function createLocalMock(fixtures) {
  const server = http.createServer(async (request, response) => {
    for await (const _chunk of request) {
      // Drain the bounded local request body without retaining it.
    }
    if (request.url === '/start') {
      response.writeHead(302, { location: '/InscripcionClaseBuscar.aspx' });
      response.end();
      return;
    }
    if (request.method === 'GET' && request.url === '/InscripcionClaseBuscar.aspx') {
      response.writeHead(200, { 'content-type': 'text/html; charset=utf-8' });
      response.end(fixtures.initial);
      return;
    }
    if (request.method === 'POST' && request.url === '/InscripcionClaseBuscar.aspx') {
      response.writeHead(200, { 'content-type': 'text/html; charset=utf-8' });
      response.end(fixtures.postback);
      return;
    }
    response.writeHead(404);
    response.end();
  });

  await new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', resolve);
  });
  const address = server.address();
  assert(address && address.address === '127.0.0.1', 'mock must bind only to 127.0.0.1');
  return {
    origin: `http://127.0.0.1:${address.port}`,
    close: () => new Promise((resolve, reject) => {
      server.closeAllConnections();
      server.close((error) => (error ? reject(error) : resolve()));
    }),
  };
}

function installPlaywrightLoadGuard() {
  const require = createRequire(import.meta.url);
  const Module = require('node:module');
  const originalLoad = Module._load;
  let detected = Object.keys(require.cache)
    .some((filename) => filename.toLowerCase().includes('playwright'));
  Module._load = function guardedLoad(request, ...rest) {
    if (String(request).toLowerCase().includes('playwright')) detected = true;
    return originalLoad.call(this, request, ...rest);
  };
  return {
    assertClear() {
      const resourceLoaded = performance.getEntriesByType('resource')
        .some(({ name }) => String(name).toLowerCase().includes('playwright'));
      assert.equal(detected || resourceLoaded, false, 'Playwright was loaded during the HTTP benchmark');
    },
    restore() {
      Module._load = originalLoad;
    },
  };
}

async function runWorker(concurrency) {
  const guard = installPlaywrightLoadGuard();
  const fixtures = await loadFixtures();
  const mock = await createLocalMock(fixtures);
  try {
    const [{ withHttpSession }, { runHttpSearch }] = await Promise.all([
      import('../src/automation/http-session.js'),
      import('../src/automation/http-search.js'),
    ]);
    const search = () => withHttpSession(
      DUMMY_CREDENTIALS,
      (session) => runHttpSearch(session, FILTERS, {
        startUrl: `${mock.origin}/start`,
        limits: { maxBodyBytes: fixtures.bytes.maxBody },
      }),
      { allowedOrigin: mock.origin, maxBodyBytes: fixtures.bytes.maxBody },
    );

    const warmup = await search();
    assert.equal(warmup.status, 'verified', 'warm-up search must verify successfully');
    guard.assertClear();
    global.gc();
    const baselineRss = process.memoryUsage.rss();
    let peakRss = baselineRss;
    const sample = () => { peakRss = Math.max(peakRss, process.memoryUsage.rss()); };
    const sampler = setInterval(sample, 1);
    let outcomes;
    try {
      outcomes = await Promise.all(Array.from({ length: concurrency }, () => search()));
      sample();
    } finally {
      clearInterval(sampler);
    }
    assert(outcomes.every(({ status }) => status === 'verified'), 'measured searches must verify successfully');
    guard.assertClear();
    const deltaRss = peakRss - baselineRss;
    const perSearchDeltaRss = deltaRss / concurrency;
    assert(perSearchDeltaRss < RSS_LIMIT_BYTES, `RSS gate failed: ${perSearchDeltaRss} bytes per search`);
    return {
      kind: 'worker-result',
      nodeVersion: process.versions.node,
      execPath: process.execPath,
      fixtureBytes: fixtures.bytes,
      concurrency,
      baselineRss,
      peakRss,
      deltaRss,
      perSearchDeltaRss,
      limitBytesExclusive: RSS_LIMIT_BYTES,
    };
  } finally {
    guard.restore();
    await mock.close();
  }
}

async function spawnWorker(concurrency) {
  const child = spawn(process.execPath, [
    '--expose-gc',
    new URL(import.meta.url).pathname.replace(/^\/(?:([A-Za-z]):)/, '$1:'),
    '--worker',
    '--scenario',
    concurrency === 1 ? 'isolated' : 'concurrent',
    '--concurrency',
    String(concurrency),
  ], {
    cwd: process.cwd(),
    env: { NODE_NO_WARNINGS: '1' },
    stdio: ['ignore', 'pipe', 'pipe'],
    windowsHide: true,
  });
  let stdout = '';
  let stderr = '';
  child.stdout.setEncoding('utf8');
  child.stderr.setEncoding('utf8');
  child.stdout.on('data', (chunk) => { stdout += chunk; });
  child.stderr.on('data', (chunk) => { stderr += chunk; });
  const exitCode = await new Promise((resolve, reject) => {
    child.once('error', reject);
    child.once('exit', resolve);
  });
  assert.equal(exitCode, 0, `worker exited ${exitCode}: ${stderr.trim()}`);
  const lines = stdout.trim().split(/\r?\n/);
  const result = JSON.parse(lines.at(-1));
  assert.equal(result.kind, 'worker-result');
  assert.equal(result.nodeVersion.split('.')[0], '24');
  assert.equal(result.execPath, process.execPath, 'worker must inherit the approved Node 24 execPath');
  return result;
}

async function runParent(options) {
  const results = [];
  if (options.scenario === 'isolated') {
    assert.equal(options.runs, 5, 'isolated scenario requires exactly five runs');
    for (let run = 1; run <= options.runs; run += 1) {
      const result = await spawnWorker(1);
      results.push({ run, ...result });
      console.log(JSON.stringify({ scenario: 'isolated', run, ...result }));
    }
    const worstDeltaRss = Math.max(...results.map(({ deltaRss }) => deltaRss));
    assert(results.every(({ deltaRss }) => deltaRss < RSS_LIMIT_BYTES), 'an isolated RSS delta reached the exclusive 10 MiB limit');
    console.log(JSON.stringify({
      scenario: 'isolated-summary',
      nodeVersion: process.versions.node,
      execPath: process.execPath,
      runs: results.length,
      fixtureBytes: results[0].fixtureBytes,
      worstDeltaRss,
      limitBytesExclusive: RSS_LIMIT_BYTES,
      status: 'pass',
    }));
    return;
  }

  assert.equal(options.concurrency, 2, 'concurrent scenario requires concurrency=2');
  const result = await spawnWorker(options.concurrency);
  assert(result.perSearchDeltaRss < RSS_LIMIT_BYTES, 'concurrent RSS delta per search reached the exclusive 10 MiB limit');
  console.log(JSON.stringify({ scenario: 'concurrent', ...result, status: 'pass' }));
}

requireNode24();
const options = parseArgs(process.argv.slice(2));
try {
  if (options.worker) {
    const result = await runWorker(options.concurrency);
    console.log(JSON.stringify(result));
  } else {
    await runParent(options);
  }
} catch (error) {
  console.error(`benchmark_failed: ${error instanceof Error ? error.message : 'unknown error'}`);
  process.exitCode = 1;
}
