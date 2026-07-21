import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import test from 'node:test';

import {
  EXPECTED_FIXTURE_PATHS,
  LIMIT_BYTES_EXCLUSIVE,
  buildEvidenceSummary,
  validateManifest,
  validateRuntime,
  validateWorkerResult,
} from './http-search-memory-contract.js';

const EXEC_PATH = process.execPath;
const VERSION = '25.8.1';

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
  return {
    kind: 'worker-result',
    nodeVersion: VERSION,
    execPath: EXEC_PATH,
    concurrency,
    deltaRss: concurrency * 1024,
    perSearchDeltaRss: 1024,
    limitBytesExclusive: LIMIT_BYTES_EXCLUSIVE,
    outcomeHash: 'a'.repeat(64),
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

test('worker result accepts the expected executable and rejects another one', () => {
  assert.equal(validateWorkerResult(worker(), { expectedExecPath: EXEC_PATH, expectedNodeVersion: VERSION }).concurrency, 1);
  assert.throws(() => validateWorkerResult(worker(), {
    expectedExecPath: `${EXEC_PATH}.other`, expectedNodeVersion: VERSION,
  }), /execPath/);
});

test('summary accepts exactly five isolated workers, their summary and concurrency two', () => {
  const isolatedRows = Array.from({ length: 5 }, (_, index) => ({ scenario: 'isolated', run: index + 1, ...worker() }));
  const isolatedSummary = {
    scenario: 'isolated-summary', nodeVersion: VERSION, execPath: EXEC_PATH, runs: 5,
    worstDeltaRss: 1024, limitBytesExclusive: LIMIT_BYTES_EXCLUSIVE, status: 'pass',
  };
  const concurrentRow = { scenario: 'concurrent', ...worker(2), status: 'pass' };
  const summary = buildEvidenceSummary({ isolatedRows, isolatedSummary, concurrentRow, sourceCommit: 'c'.repeat(40) });
  assert.equal(summary.pass, true);
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
