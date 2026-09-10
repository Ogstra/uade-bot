import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { EventEmitter } from 'node:events';
import { createHash } from 'node:crypto';
import { existsSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { PassThrough } from 'node:stream';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

import {
  EXPECTED_FIXTURE_PATHS,
  LIMIT_BYTES_EXCLUSIVE,
  MEMORY_SOURCE_SCOPE,
  REQUIRED_CHECKPOINT_STAGES,
  buildEvidenceSummary,
  validateManifest,
  validateRuntime,
  validateWorkerResult,
} from './http-search-memory-contract.js';
import {
  validateManifestShape as validateIndependentManifestShape,
  MEMORY_SOURCE_SCOPE as INDEPENDENT_MEMORY_SOURCE_SCOPE,
  validateScopedGitStatus as validateIndependentScopedGitStatus,
  validateWorker as validateIndependentWorker,
} from './validate-http-search-memory-evidence.js';
import { collectWorkerResult, positiveInteger } from './benchmark-http-search-memory.js';

const EXEC_PATH = process.execPath;
const VERSION = '25.8.1';
const REPO_ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const PWSH_PATH = process.platform === 'win32'
  ? path.join(process.env.ProgramFiles ?? 'C:\\Program Files', 'PowerShell', '7', 'pwsh.exe')
  : 'pwsh';

function fixtureRecords() {
  return EXPECTED_FIXTURE_PATHS.map((path, index) => {
    const content = Buffer.from(`fixture-${index}`);
    return {
      path,
      bytes: content.byteLength,
      sha256: createHash('sha256').update(content).digest('hex'),
    };
  });
}

function manifest(records = fixtureRecords()) {
  return {
    schemaVersion: 1,
    baselineKind: 'rebaseline-retained-contract',
    syntheticEnvelope: true,
    postbackModeAccepted: 'accepted',
    responses: {
      initialGet: { bytes: 87340 },
      fullPostback: { bytes: 87340 },
      asyncPostback: { bytes: 87340 },
    },
    derivedLimits: {
      measuredMaxBodyBytes: 87340,
      measuredDeltaChars: 80154,
      measuredDeltaNodes: 17,
      maxBodyBytes: 300000,
      maxDeltaChars: 100193,
      maxDeltaNodes: 22,
    },
    fixtures: records,
  };
}

function worker(concurrency = 1) {
  const vacancy = { turno: 'NOCHE', sede: 'MONSERRAT', horario: '18:45 22:15', dias: ['LU', 'MI'], cupos: 1 };
  const outcomes = Array.from({ length: concurrency }, () => ({ outcome: 'found', vacancies: [{ ...vacancy, dias: [...vacancy.dias] }] }));
  const baselineRss = 100000;
  const deltaRss = concurrency * 1024;
  const peakRss = baselineRss + deltaRss;
  return {
    kind: 'worker-result',
    nodeVersion: VERSION,
    execPath: EXEC_PATH,
    concurrency,
    baselineRss,
    peakRss,
    deltaRss,
    perSearchDeltaRss: 1024,
    checkpointRss: Object.fromEntries(REQUIRED_CHECKPOINT_STAGES.map((stage) => [stage, peakRss])),
    limitBytesExclusive: LIMIT_BYTES_EXCLUSIVE,
    outcomes,
    outcomeHash: createHash('sha256').update(JSON.stringify(outcomes)).digest('hex'),
    inputManifestHash: 'b'.repeat(64),
  };
}

test('runtime accepts Node major 24 or higher with exposed GC and matching engine contract', () => {
  assert.deepEqual(validateRuntime({
    nodeVersion: VERSION,
    execPath: EXEC_PATH,
    gcAvailable: true,
    engineRange: '>=24',
  }), { nodeVersion: VERSION, nodeMajor: 25, execPath: EXEC_PATH });
});

test('runtime rejects Node older than 24', () => {
  assert.throws(() => validateRuntime({
    nodeVersion: '23.11.0', execPath: EXEC_PATH, gcAvailable: true, engineRange: '>=24',
  }), /major 24 or newer/);
});

test('runtime rejects missing exposed GC', () => {
  assert.throws(() => validateRuntime({
    nodeVersion: VERSION, execPath: EXEC_PATH, gcAvailable: false, engineRange: '>=24',
  }), /--expose-gc/);
});

test('manifest accepts the exact rebaseline, allowlist, hashes, bytes and limits', () => {
  assert.equal(validateManifest(manifest(), fixtureRecords()).maxBodyBytes, 300000);
});

test('manifest rejects a different baseline declaration', () => {
  const changed = manifest();
  changed.baselineKind = 'captured-live';
  assert.throws(() => validateManifest(changed, fixtureRecords()), /baselineKind/);
});

test('manifest rejects an altered fixture byte or hash', () => {
  const records = fixtureRecords();
  records[0] = { ...records[0], bytes: records[0].bytes + 1 };
  assert.throws(() => validateManifest(manifest(), records), /fixture .* bytes/);
  const hashChanged = fixtureRecords();
  hashChanged[0] = { ...hashChanged[0], sha256: '0'.repeat(64) };
  assert.throws(() => validateManifest(manifest(), hashChanged), /fixture .* sha256/);
});

test('positive integer parser rejects suffixes, decimals, signs and leading zeroes', () => {
  assert.equal(positiveInteger('5', 'runs'), 5);
  for (const invalid of ['5junk', '2.9', '+2', '01', '0', '-1']) {
    assert.throws(() => positiveInteger(invalid, 'runs'), /positive integer/);
  }
});

test('worker collection waits for close before parsing the complete stdout stream', async () => {
  const child = new EventEmitter();
  child.stdout = new PassThrough();
  child.stderr = new PassThrough();
  const expected = worker();
  const collected = collectWorkerResult(child, { expectedExecPath: EXEC_PATH, expectedNodeVersion: VERSION });
  child.emit('exit', 0, null);
  child.stdout.write(`${JSON.stringify(expected).slice(0, -1)}`);
  child.stdout.write('}\n');
  child.stdout.end();
  child.stderr.end();
  child.emit('close', 0, null);
  assert.deepEqual(await collected, expected);
});

test('benchmark wrapper invalidates an old pass before a preflight failure', {
  skip: !existsSync(PWSH_PATH),
}, () => {
  const outputDir = mkdtempSync(path.join(tmpdir(), 'memory-wrapper-failure-'));
  const summaryPath = path.join(outputDir, 'summary.json');
  writeFileSync(summaryPath, '{"pass":true}\n');
  try {
    const result = spawnSync(PWSH_PATH, [
      '-NoProfile',
      '-File', path.join(REPO_ROOT, 'scripts', 'run-http-search-memory-benchmark.ps1'),
      '-NodeBin', path.join(outputDir, 'missing-node.exe'),
      '-OutputDir', outputDir,
    ], { encoding: 'utf8' });
    assert.notEqual(result.status, 0, 'preflight unexpectedly succeeded');
    assert.equal(existsSync(summaryPath), false, 'stale pass summary remained visible');
  } finally {
    rmSync(outputDir, { recursive: true, force: true });
  }
});

const manifestMutations = [
  ['schemaVersion', (value) => { value.schemaVersion = 2; }],
  ['initialGet bytes', (value) => { value.responses.initialGet.bytes += 1; }],
  ['fullPostback bytes', (value) => { value.responses.fullPostback.bytes += 1; }],
  ['asyncPostback bytes', (value) => { value.responses.asyncPostback.bytes += 1; }],
  ['measuredMaxBodyBytes', (value) => { value.derivedLimits.measuredMaxBodyBytes += 1; }],
  ['measuredDeltaChars', (value) => { value.derivedLimits.measuredDeltaChars += 1; }],
  ['measuredDeltaNodes', (value) => { value.derivedLimits.measuredDeltaNodes += 1; }],
  ['maxBodyBytes', (value) => { value.derivedLimits.maxBodyBytes += 1; }],
  ['maxDeltaChars', (value) => { value.derivedLimits.maxDeltaChars += 1; }],
  ['maxDeltaNodes', (value) => { value.derivedLimits.maxDeltaNodes += 1; }],
];

for (const [label, mutate] of manifestMutations) {
  test(`both manifest validators reject altered ${label}`, () => {
    const changed = manifest();
    mutate(changed);
    assert.throws(() => validateManifest(changed, fixtureRecords()), new RegExp(label.split(' ')[0], 'i'));
    assert.throws(() => validateIndependentManifestShape(changed));
  });
}

test('worker result accepts the expected executable and rejects another one', () => {
  assert.equal(validateWorkerResult(worker(), { expectedExecPath: EXEC_PATH, expectedNodeVersion: VERSION }).concurrency, 1);
  assert.throws(() => validateWorkerResult(worker(), {
    expectedExecPath: `${EXEC_PATH}.other`, expectedNodeVersion: VERSION,
  }), /execPath/);
});

for (const [label, mutate] of [
  ['baselineRss', (value) => { value.baselineRss += 1; }],
  ['peakRss', (value) => { value.peakRss += 1; }],
  ['deltaRss', (value) => { value.deltaRss += 1; }],
  ['perSearchDeltaRss', (value) => { value.perSearchDeltaRss += 1; }],
  ['limitBytesExclusive', (value) => { value.limitBytesExclusive += 1; }],
  ['outcomeHash', (value) => { value.outcomeHash = 'a'.repeat(64); }],
  ['outcomes', (value) => { value.outcomes[0].outcome = 'no_vacancies'; }],
]) {
  test(`both worker validators reject altered ${label}`, () => {
    const changed = worker(2);
    mutate(changed);
    const runtime = { expectedExecPath: EXEC_PATH, expectedNodeVersion: VERSION };
    assert.throws(() => validateWorkerResult(changed, runtime), new RegExp(label.replace('BytesExclusive', '|threshold'), 'i'));
    assert.throws(() => validateIndependentWorker(changed, { execPath: EXEC_PATH, nodeVersion: VERSION }, 2));
  });
}

test('both worker validators recompute and enforce the fixed exclusive per-search threshold', () => {
  const changed = worker();
  changed.baselineRss = 0;
  changed.peakRss = LIMIT_BYTES_EXCLUSIVE;
  changed.deltaRss = LIMIT_BYTES_EXCLUSIVE;
  changed.perSearchDeltaRss = LIMIT_BYTES_EXCLUSIVE;
  changed.checkpointRss = Object.fromEntries(REQUIRED_CHECKPOINT_STAGES.map((stage) => [stage, LIMIT_BYTES_EXCLUSIVE]));
  const runtime = { expectedExecPath: EXEC_PATH, expectedNodeVersion: VERSION };
  assert.throws(() => validateWorkerResult(changed, runtime), /exclusive 10 MiB limit/);
  assert.throws(() => validateIndependentWorker(changed, { execPath: EXEC_PATH, nodeVersion: VERSION }, 1), /exclusive 10 MiB threshold/);
});

for (const [label, replacement] of [
  ['found without vacancies', { outcome: 'found' }],
  ['found with empty vacancies', { outcome: 'found', vacancies: [] }],
  ['found with an invalid vacancy', { outcome: 'found', vacancies: [{ cupos: 1 }] }],
  ['found with an unexpected field', { outcome: 'found', vacancies: [{ turno: 'NOCHE', sede: 'MONSERRAT', horario: '18:45', dias: ['LU'], cupos: 1 }], secret: 'extra' }],
  ['a valid non-found outcome for this retained fixture', { outcome: 'no_vacancies' }],
]) {
  test(`both worker validators reject ${label} before accepting its recomputed hash`, () => {
    const changed = worker();
    changed.outcomes = [replacement];
    changed.outcomeHash = createHash('sha256').update(JSON.stringify(changed.outcomes)).digest('hex');
    const runtime = { expectedExecPath: EXEC_PATH, expectedNodeVersion: VERSION };
    assert.throws(() => validateWorkerResult(changed, runtime), /SearchOutcomeSchema|outside|must be found|vacancies/i);
    assert.throws(() => validateIndependentWorker(changed, { execPath: EXEC_PATH, nodeVersion: VERSION }, 1), /SearchOutcomeSchema|outside|must be found|vacancies/i);
  });
}

for (const [label, mutate] of [
  ['missing checkpoint object', (value) => { delete value.checkpointRss; }],
  ['empty checkpoint object', (value) => { value.checkpointRss = {}; }],
  ['missing required stage', (value) => { delete value.checkpointRss.search_chain_complete; }],
  ['negative checkpoint', (value) => { value.checkpointRss.interval = -1; }],
  ['non-integer checkpoint', (value) => { value.checkpointRss.interval = 1.5; }],
  ['unknown checkpoint stage', (value) => { value.checkpointRss.unapproved = value.peakRss; }],
  ['checkpoint above peak', (value) => { value.checkpointRss.interval = 999999999; }],
  ['peak not equal to retained maximum', (value) => {
    for (const stage of Object.keys(value.checkpointRss)) value.checkpointRss[stage] -= 1;
  }],
]) {
  test(`both worker validators reject ${label}`, () => {
    const changed = worker(2);
    mutate(changed);
    const runtime = { expectedExecPath: EXEC_PATH, expectedNodeVersion: VERSION };
    assert.throws(() => validateWorkerResult(changed, runtime), /checkpoint|peakRss|maximum retained/i);
    assert.throws(() => validateIndependentWorker(changed, { execPath: EXEC_PATH, nodeVersion: VERSION }, 2), /checkpoint|peakRss|maximum retained/i);
  });
}

test('summary accepts exactly five isolated workers, their summary and concurrency two', () => {
  const isolatedRows = Array.from({ length: 5 }, (_, index) => ({ scenario: 'isolated', run: index + 1, ...worker() }));
  const isolatedSummary = {
    scenario: 'isolated-summary', nodeVersion: VERSION, execPath: EXEC_PATH, runs: 5,
    worstDeltaRss: 1024, limitBytesExclusive: LIMIT_BYTES_EXCLUSIVE, status: 'pass',
  };
  const concurrentRow = { scenario: 'concurrent', ...worker(2), status: 'pass' };
  const summary = buildEvidenceSummary({ isolatedRows, isolatedSummary, concurrentRow, sourceCommit: 'c'.repeat(40) });
  assert.equal(summary.pass, true);
  assert.deepEqual(summary.sourceScope, MEMORY_SOURCE_SCOPE);
  assert.deepEqual(summary.sourceScope, INDEPENDENT_MEMORY_SOURCE_SCOPE);
  assert.equal(summary.isolated.runs, 5);
  assert.equal(summary.concurrent.concurrency, 2);
});

test('summary rejects incomplete or failed evidence and cannot publish pass', () => {
  const rows = Array.from({ length: 4 }, (_, index) => ({ scenario: 'isolated', run: index + 1, ...worker() }));
  const isolatedSummary = {
    scenario: 'isolated-summary', nodeVersion: VERSION, execPath: EXEC_PATH, runs: 5,
    worstDeltaRss: 1024, limitBytesExclusive: LIMIT_BYTES_EXCLUSIVE, status: 'pass',
  };
  const concurrentRow = { scenario: 'concurrent', ...worker(2), status: 'pass' };
  assert.throws(() => buildEvidenceSummary({ isolatedRows: rows, isolatedSummary, concurrentRow, sourceCommit: 'c'.repeat(40) }), /exactly five/);
  assert.throws(() => buildEvidenceSummary({
    isolatedRows: [...rows, { scenario: 'isolated', run: 5, ...worker() }],
    isolatedSummary,
    concurrentRow: { ...concurrentRow, status: 'fail' },
    sourceCommit: 'c'.repeat(40),
  }), /status pass/);
});

test('summary rejects inconsistent functional outcomes across isolated runs', () => {
  const isolatedRows = Array.from({ length: 5 }, (_, index) => ({ scenario: 'isolated', run: index + 1, ...worker() }));
  isolatedRows[4] = { ...isolatedRows[4], ...worker() };
  isolatedRows[4].outcomes[0].vacancies[0].cupos = 2;
  isolatedRows[4].outcomeHash = createHash('sha256').update(JSON.stringify(isolatedRows[4].outcomes)).digest('hex');
  const isolatedSummary = {
    scenario: 'isolated-summary', nodeVersion: VERSION, execPath: EXEC_PATH, runs: 5,
    worstDeltaRss: 1024, limitBytesExclusive: LIMIT_BYTES_EXCLUSIVE, status: 'pass',
  };
  assert.throws(() => buildEvidenceSummary({
    isolatedRows,
    isolatedSummary,
    concurrentRow: { scenario: 'concurrent', ...worker(2), status: 'pass' },
    sourceCommit: 'c'.repeat(40),
  }), /outcome hashes differ/);
});

test('memory scope policy rejects staged, unstaged and untracked changes without requiring a globally clean tree', () => {
  for (const status of [
    'M  src/automation/http-search.js',
    ' M scripts/benchmark-http-search-memory.js',
    '?? src/automation/untracked.js',
  ]) {
    assert.throws(() => validateIndependentScopedGitStatus(status), /differs from HEAD/i);
  }
  assert.doesNotThrow(() => validateIndependentScopedGitStatus(''));
});

test('summary rejects a concurrent outcome that differs from the isolated canonical outcome', () => {
  const isolatedRows = Array.from({ length: 5 }, (_, index) => ({ scenario: 'isolated', run: index + 1, ...worker() }));
  const isolatedSummary = {
    scenario: 'isolated-summary', nodeVersion: VERSION, execPath: EXEC_PATH, runs: 5,
    worstDeltaRss: 1024, limitBytesExclusive: LIMIT_BYTES_EXCLUSIVE, status: 'pass',
  };
  const concurrentRow = { scenario: 'concurrent', ...worker(2), status: 'pass' };
  concurrentRow.outcomes[1].vacancies[0].cupos = 2;
  concurrentRow.outcomeHash = createHash('sha256').update(JSON.stringify(concurrentRow.outcomes)).digest('hex');
  assert.throws(() => buildEvidenceSummary({
    isolatedRows, isolatedSummary, concurrentRow, sourceCommit: 'c'.repeat(40),
  }), /concurrent functional outcomes differ/);
});
