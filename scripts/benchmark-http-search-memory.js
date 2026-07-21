import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { createHash } from 'node:crypto';
import { readFile } from 'node:fs/promises';
import http from 'node:http';
import { createRequire } from 'node:module';
import { performance } from 'node:perf_hooks';
import { fileURLToPath } from 'node:url';

import {
  EXPECTED_FIXTURE_PATHS,
  LIMIT_BYTES_EXCLUSIVE,
  buildEvidenceSummary,
  validateManifest,
  validateRuntime,
  validateWorkerResult,
} from './http-search-memory-contract.js';

const REPO_ROOT = new URL('../', import.meta.url);
const MANIFEST_URL = new URL('.planning/phases/03.2-motor-http-sin-navegador/evidence/webforms-capture-sanitized/manifest.json', REPO_ROOT);
const PACKAGE_URL = new URL('package.json', REPO_ROOT);
const FILTERS = Object.freeze({
  materiaCodigo: '3.1.050',
  ofrecimiento: 'curricular',
  turno: 'mañana',
  dias: ['LU', 'MI'],
  sedesExcluidas: [],
});
const DUMMY_CREDENTIALS = Object.freeze({ username: 'benchmark-user', password: 'benchmark-password' });

function sha256(value) {
  return createHash('sha256').update(value).digest('hex');
}

function positiveInteger(value, name) {
  const parsed = Number.parseInt(value, 10);
  assert(Number.isSafeInteger(parsed) && parsed > 0, `${name} must be a positive integer`);
  return parsed;
}

function parseArgs(argv) {
  const values = new Map();
  let worker = false;
  let aggregate = false;
  for (let index = 0; index < argv.length; index += 1) {
    if (argv[index] === '--worker' || argv[index] === '--aggregate') {
      worker ||= argv[index] === '--worker';
      aggregate ||= argv[index] === '--aggregate';
      continue;
    }
    const name = argv[index];
    const value = argv[index + 1];
    assert(name?.startsWith('--') && value && !value.startsWith('--'), `invalid argument ${name ?? ''}`);
    values.set(name.slice(2), value);
    index += 1;
  }
  if (aggregate) {
    for (const required of ['isolated-file', 'concurrent-file', 'source-commit']) {
      assert(values.has(required), `--${required} is required for aggregation`);
    }
    return {
      aggregate,
      isolatedFile: values.get('isolated-file'),
      concurrentFile: values.get('concurrent-file'),
      sourceCommit: values.get('source-commit'),
    };
  }
  const scenario = values.get('scenario');
  assert(['isolated', 'concurrent'].includes(scenario), 'scenario must be isolated or concurrent');
  return {
    worker,
    aggregate,
    scenario,
    runs: values.has('runs') ? positiveInteger(values.get('runs'), 'runs') : 1,
    concurrency: values.has('concurrency') ? positiveInteger(values.get('concurrency'), 'concurrency') : 1,
  };
}

function padToApprovedBytes(html, approvedBytes, label) {
  const currentBytes = Buffer.byteLength(html);
  assert(currentBytes <= approvedBytes, `${label} exceeds its approved envelope`);
  return html + ' '.repeat(approvedBytes - currentBytes);
}

async function loadFixtures() {
  const manifestBytes = await readFile(MANIFEST_URL);
  const manifest = JSON.parse(manifestBytes.toString('utf8'));
  const fixtureBuffers = await Promise.all(EXPECTED_FIXTURE_PATHS.map((fixturePath) => readFile(new URL(fixturePath, REPO_ROOT))));
  const records = EXPECTED_FIXTURE_PATHS.map((fixturePath, index) => ({
    path: fixturePath,
    bytes: fixtureBuffers[index].byteLength,
    sha256: sha256(fixtureBuffers[index]),
  }));
  const limits = validateManifest(manifest, records);
  const byPath = new Map(EXPECTED_FIXTURE_PATHS.map((fixturePath, index) => [fixturePath, fixtureBuffers[index].toString('utf8')]));
  const resultTable = byPath.get('src/automation/__fixtures__/results-sample.html')
    .match(/<table\b[^>]*class="grillaInscripcion"[\s\S]*?<\/table>/)?.[0]
    ?.replace(/(<input[^>]+id="[^"]*hiddenLU_0")>/, '$1 value="True">')
    .replace(/(<input[^>]+id="[^"]*hiddenMI_0")>/, '$1 value="True">');
  assert(resultTable, 'results fixture must contain the production results table');
  const postbackTemplate = byPath.get('src/automation/__fixtures__/webforms/postback-found.html');
  const composedPostback = postbackTemplate.replace(/<table\s+id="results">[\s\S]*?<\/table>/, resultTable);
  return {
    initial: padToApprovedBytes(byPath.get('src/automation/__fixtures__/webforms/initial-form.html'), limits.envelopeBytes, 'initial fixture'),
    postback: padToApprovedBytes(composedPostback, limits.envelopeBytes, 'postback fixture'),
    limits,
    manifestHash: sha256(manifestBytes),
  };
}

async function createLocalMock(fixtures) {
  const server = http.createServer(async (request, response) => {
    for await (const _chunk of request) {
      // Drain the bounded local request without retaining it.
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
  let detected = Object.keys(require.cache).some((filename) => filename.toLowerCase().includes('playwright'));
  Module._load = function guardedLoad(request, ...rest) {
    if (String(request).toLowerCase().includes('playwright')) detected = true;
    return originalLoad.call(this, request, ...rest);
  };
  return {
    assertClear() {
      const resourceLoaded = performance.getEntriesByType('resource').some(({ name }) => String(name).toLowerCase().includes('playwright'));
      assert.equal(detected || resourceLoaded, false, 'Playwright was loaded during the HTTP benchmark');
    },
    restore() { Module._load = originalLoad; },
  };
}

async function runWorker(concurrency) {
  const guard = installPlaywrightLoadGuard();
  const fixtures = await loadFixtures();
  const mock = await createLocalMock(fixtures);
  try {
    const [{ withHttpSession }, { runHttpSearch }, { parseResults, filterVacancies }, { classifySearchResult }] = await Promise.all([
      import('../src/automation/http-session.js'),
      import('../src/automation/http-search.js'),
      import('../src/automation/parse-results.js'),
      import('../src/automation/classify.js'),
    ]);
    const search = (memoryCheckpoint = () => {}) => withHttpSession(
      DUMMY_CREDENTIALS,
      async (session) => {
        const result = await runHttpSearch(session, FILTERS, {
          startUrl: `${mock.origin}/start`,
          limits: {
            maxBodyBytes: fixtures.limits.maxBodyBytes,
            maxDeltaChars: fixtures.limits.maxDeltaChars,
            maxDeltaNodes: fixtures.limits.maxDeltaNodes,
          },
          memoryCheckpoint,
        });
        if (result.status !== 'verified') {
          return classifySearchResult({ searchStatus: result.status, reason: result.reason, vacancies: [] });
        }
        const parsed = await parseResults(result.html, { memoryCheckpoint });
        if (parsed.invalidRowCount > 0 || !parsed.resultsContainerDetected) {
          return classifySearchResult({ searchStatus: 'search_failed', reason: 'result_parse_failed', vacancies: [] });
        }
        const vacancies = filterVacancies(parsed.rows, FILTERS);
        memoryCheckpoint('vacancy_filter_complete');
        const outcome = classifySearchResult({ searchStatus: 'verified', vacancies });
        memoryCheckpoint('classification_complete');
        return outcome;
      },
      { allowedOrigin: mock.origin, maxBodyBytes: fixtures.limits.maxBodyBytes },
    );
    const warmup = await search();
    assert.equal(warmup.outcome, 'found', 'warm-up production search chain must find a validated vacancy');
    guard.assertClear();
    global.gc();
    const baselineRss = process.memoryUsage.rss();
    let peakRss = baselineRss;
    const checkpointRss = {};
    const sample = (stage = 'interval') => {
      const rss = process.memoryUsage.rss();
      peakRss = Math.max(peakRss, rss);
      checkpointRss[stage] = Math.max(checkpointRss[stage] ?? 0, rss);
    };
    const sampler = setInterval(() => sample(), 1);
    let outcomes;
    try {
      outcomes = await Promise.all(Array.from({ length: concurrency }, () => search(sample)));
      sample('search_chain_complete');
    } finally {
      clearInterval(sampler);
    }
    assert(outcomes.every(({ outcome }) => outcome === 'found'), 'measured production search chains must find validated vacancies');
    guard.assertClear();
    const deltaRss = peakRss - baselineRss;
    const perSearchDeltaRss = deltaRss / concurrency;
    assert(perSearchDeltaRss < LIMIT_BYTES_EXCLUSIVE, `RSS gate failed: ${perSearchDeltaRss} bytes per search`);
    return {
      kind: 'worker-result',
      nodeVersion: process.versions.node,
      execPath: process.execPath,
      concurrency,
      baselineRss,
      peakRss,
      deltaRss,
      perSearchDeltaRss,
      checkpointRss,
      limitBytesExclusive: LIMIT_BYTES_EXCLUSIVE,
      inputManifestHash: fixtures.manifestHash,
      outcomes,
      outcomeHash: sha256(JSON.stringify(outcomes)),
    };
  } finally {
    guard.restore();
    await mock.close();
  }
}

async function spawnWorker(concurrency) {
  const child = spawn(process.execPath, [
    '--expose-gc', fileURLToPath(import.meta.url), '--worker', '--scenario',
    concurrency === 1 ? 'isolated' : 'concurrent', '--concurrency', String(concurrency),
  ], {
    cwd: fileURLToPath(REPO_ROOT),
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
  const result = JSON.parse(stdout.trim().split(/\r?\n/).at(-1));
  return validateWorkerResult(result, { expectedExecPath: process.execPath, expectedNodeVersion: process.versions.node });
}

async function runParent(options) {
  if (options.scenario === 'isolated') {
    assert.equal(options.runs, 5, 'isolated scenario requires exactly five runs');
    const results = [];
    for (let run = 1; run <= options.runs; run += 1) {
      const result = await spawnWorker(1);
      results.push({ run, ...result });
      console.log(JSON.stringify({ scenario: 'isolated', run, ...result }));
    }
    const worstDeltaRss = Math.max(...results.map(({ deltaRss }) => deltaRss));
    assert(results.every(({ deltaRss }) => deltaRss < LIMIT_BYTES_EXCLUSIVE), 'an isolated RSS delta reached the exclusive 10 MiB limit');
    console.log(JSON.stringify({
      scenario: 'isolated-summary', nodeVersion: process.versions.node, execPath: process.execPath,
      runs: 5, worstDeltaRss, limitBytesExclusive: LIMIT_BYTES_EXCLUSIVE, status: 'pass',
    }));
    return;
  }
  assert.equal(options.concurrency, 2, 'concurrent scenario requires concurrency=2');
  const result = await spawnWorker(2);
  console.log(JSON.stringify({ scenario: 'concurrent', ...result, status: 'pass' }));
}

function parseJsonl(text, label) {
  const lines = text.trim().split(/\r?\n/).filter(Boolean);
  assert(lines.length > 0, `${label} JSONL is empty`);
  return lines.map((line, index) => {
    try { return JSON.parse(line); } catch { throw new Error(`${label} line ${index + 1} is not valid JSON`); }
  });
}

async function aggregateEvidence(options) {
  const [isolatedBytes, concurrentBytes] = await Promise.all([
    readFile(options.isolatedFile), readFile(options.concurrentFile),
  ]);
  const isolated = parseJsonl(isolatedBytes.toString('utf8'), 'isolated');
  const concurrent = parseJsonl(concurrentBytes.toString('utf8'), 'concurrent');
  assert.equal(concurrent.length, 1, 'concurrent evidence must contain exactly one row');
  return buildEvidenceSummary({
    isolatedRows: isolated.filter(({ scenario }) => scenario === 'isolated'),
    isolatedSummary: isolated.find(({ scenario }) => scenario === 'isolated-summary'),
    concurrentRow: concurrent[0],
    sourceCommit: options.sourceCommit,
    outputHashes: { isolatedJsonl: sha256(isolatedBytes), concurrentJsonl: sha256(concurrentBytes) },
  });
}

const packageJson = JSON.parse(await readFile(PACKAGE_URL, 'utf8'));
validateRuntime({
  nodeVersion: process.versions.node,
  execPath: process.execPath,
  gcAvailable: typeof global.gc === 'function',
  engineRange: packageJson.engines?.node,
});
const options = parseArgs(process.argv.slice(2));
try {
  if (options.aggregate) console.log(JSON.stringify(await aggregateEvidence(options), null, 2));
  else if (options.worker) console.log(JSON.stringify(await runWorker(options.concurrency)));
  else await runParent(options);
} catch (error) {
  console.error(`benchmark_failed: ${error instanceof Error ? error.message : 'unknown error'}`);
  process.exitCode = 1;
}
