import assert from 'node:assert/strict';
import path from 'node:path';

export const LIMIT_BYTES_EXCLUSIVE = 10 * 1024 * 1024;
export const EXPECTED_FIXTURE_PATHS = Object.freeze([
  'src/automation/__fixtures__/webforms/initial-form.html',
  'src/automation/__fixtures__/webforms/postback-found.html',
  'src/automation/__fixtures__/webforms/postback-empty.html',
  'src/automation/__fixtures__/webforms/postback-mismatch.html',
  'src/automation/__fixtures__/webforms/delta-found.txt',
  'src/automation/__fixtures__/webforms/delta-empty.txt',
  'src/automation/__fixtures__/webforms/delta-error.txt',
  'src/automation/__fixtures__/webforms/delta-malformed.txt',
  'src/automation/__fixtures__/webforms/delta-mismatch.txt',
  'src/automation/__fixtures__/webforms/delta-redirect.txt',
  'src/automation/__fixtures__/results-sample.html',
]);

const HEX_64 = /^[a-f0-9]{64}$/;

function canonicalPath(value) {
  assert.equal(typeof value, 'string', 'execPath must be a string');
  assert(path.isAbsolute(value), 'execPath must be absolute');
  const normalized = path.normalize(value);
  return process.platform === 'win32' ? normalized.toLowerCase() : normalized;
}

function assertPositiveInteger(value, label) {
  assert(Number.isSafeInteger(value) && value > 0, `${label} must be a positive integer`);
}

export function validateRuntime({ nodeVersion, execPath, gcAvailable, engineRange }) {
  assert.equal(engineRange, '>=24', 'package engines.node must be >=24');
  const major = Number.parseInt(String(nodeVersion).split('.')[0], 10);
  assert(Number.isSafeInteger(major) && major >= 24, `runtime requires Node major 24 or newer; received ${nodeVersion}`);
  assert.equal(gcAvailable, true, 'runtime requires --expose-gc');
  canonicalPath(execPath);
  return { nodeVersion, nodeMajor: major, execPath };
}

export function validateManifest(manifest, fixtureRecords) {
  assert.equal(manifest?.schemaVersion, 1, 'manifest schemaVersion must be 1');
  assert.equal(manifest?.baselineKind, 'rebaseline-retained-contract', 'manifest baselineKind is not the approved rebaseline');
  assert.equal(manifest?.syntheticEnvelope, true, 'manifest syntheticEnvelope must be true');
  assert.equal(manifest?.postbackModeAccepted, 'accepted', 'manifest postbackModeAccepted must be accepted');
  for (const response of ['initialGet', 'fullPostback', 'asyncPostback']) {
    assert.equal(manifest?.responses?.[response]?.bytes, 87340, `${response} envelope must be 87340 bytes`);
  }
  assert.equal(manifest?.derivedLimits?.measuredMaxBodyBytes, 87340, 'measuredMaxBodyBytes must be 87340');
  assert.equal(manifest?.derivedLimits?.maxBodyBytes, 300000, 'maxBodyBytes must remain the independent 300000-byte cap');
  assert(Array.isArray(manifest?.fixtures), 'manifest fixtures must be an array');
  assert(Array.isArray(fixtureRecords), 'fixture records must be an array');
  assert.deepEqual(manifest.fixtures.map(({ path: fixturePath }) => fixturePath), EXPECTED_FIXTURE_PATHS, 'manifest fixture allowlist changed');
  assert.deepEqual(fixtureRecords.map(({ path: fixturePath }) => fixturePath), EXPECTED_FIXTURE_PATHS, 'loaded fixture allowlist changed');
  for (let index = 0; index < EXPECTED_FIXTURE_PATHS.length; index += 1) {
    const declared = manifest.fixtures[index];
    const actual = fixtureRecords[index];
    assertPositiveInteger(declared.bytes, `manifest fixture ${declared.path} bytes`);
    assert(HEX_64.test(declared.sha256), `manifest fixture ${declared.path} sha256 is invalid`);
    assert.equal(actual.bytes, declared.bytes, `fixture ${declared.path} bytes do not match manifest`);
    assert.equal(actual.sha256, declared.sha256, `fixture ${declared.path} sha256 does not match manifest`);
  }
  return {
    envelopeBytes: 87340,
    maxBodyBytes: 300000,
    maxDeltaChars: manifest.derivedLimits.maxDeltaChars,
    maxDeltaNodes: manifest.derivedLimits.maxDeltaNodes,
  };
}

export function validateWorkerResult(result, { expectedExecPath, expectedNodeVersion }) {
  assert.equal(result?.kind, 'worker-result', 'worker kind must be worker-result');
  assert.equal(result.nodeVersion, expectedNodeVersion, 'worker nodeVersion differs from parent');
  assert.equal(canonicalPath(result.execPath), canonicalPath(expectedExecPath), 'worker execPath differs from approved execPath');
  assert([1, 2].includes(result.concurrency), 'worker concurrency must be one or two');
  assert.equal(result.limitBytesExclusive, LIMIT_BYTES_EXCLUSIVE, 'worker memory limit changed');
  assert(Number.isFinite(result.deltaRss) && result.deltaRss >= 0, 'worker deltaRss must be non-negative');
  assert(Number.isFinite(result.perSearchDeltaRss) && result.perSearchDeltaRss >= 0, 'worker perSearchDeltaRss must be non-negative');
  assert(result.perSearchDeltaRss < LIMIT_BYTES_EXCLUSIVE, 'worker reached the exclusive 10 MiB limit');
  assert(HEX_64.test(result.outcomeHash), 'worker outcomeHash is invalid');
  assert(HEX_64.test(result.inputManifestHash), 'worker inputManifestHash is invalid');
  return result;
}

export function buildEvidenceSummary({
  isolatedRows,
  isolatedSummary,
  concurrentRow,
  sourceCommit,
  outputHashes = {},
}) {
  assert(Array.isArray(isolatedRows) && isolatedRows.length === 5, 'evidence requires exactly five isolated worker results');
  assert.deepEqual(isolatedRows.map(({ run }) => run), [1, 2, 3, 4, 5], 'isolated run numbers must be exactly 1..5');
  const runtime = { expectedExecPath: isolatedRows[0].execPath, expectedNodeVersion: isolatedRows[0].nodeVersion };
  isolatedRows.forEach((row) => {
    assert.equal(row.scenario, 'isolated', 'isolated row scenario is invalid');
    validateWorkerResult(row, runtime);
    assert.equal(row.concurrency, 1, 'isolated worker concurrency must be one');
  });
  assert.equal(isolatedSummary?.scenario, 'isolated-summary', 'isolated-summary row is missing');
  assert.equal(isolatedSummary?.status, 'pass', 'isolated-summary must have status pass');
  assert.equal(isolatedSummary?.runs, 5, 'isolated-summary must report five runs');
  assert.equal(isolatedSummary?.nodeVersion, runtime.expectedNodeVersion, 'isolated-summary nodeVersion differs');
  assert.equal(canonicalPath(isolatedSummary?.execPath), canonicalPath(runtime.expectedExecPath), 'isolated-summary execPath differs');
  assert.equal(isolatedSummary?.limitBytesExclusive, LIMIT_BYTES_EXCLUSIVE, 'isolated-summary memory limit changed');
  assert.equal(isolatedSummary?.worstDeltaRss, Math.max(...isolatedRows.map(({ deltaRss }) => deltaRss)), 'isolated-summary maximum is incorrect');
  assert.equal(concurrentRow?.scenario, 'concurrent', 'concurrent row is missing');
  assert.equal(concurrentRow?.status, 'pass', 'concurrent row must have status pass');
  validateWorkerResult(concurrentRow, runtime);
  assert.equal(concurrentRow.concurrency, 2, 'concurrent worker concurrency must be two');
  assert(/^[a-f0-9]{40}$/.test(sourceCommit), 'sourceCommit must be a full Git SHA-1');
  const inputManifestHash = isolatedRows[0].inputManifestHash;
  assert(isolatedRows.every((row) => row.inputManifestHash === inputManifestHash), 'isolated input manifest hashes differ');
  assert.equal(concurrentRow.inputManifestHash, inputManifestHash, 'concurrent input manifest hash differs');
  return {
    schemaVersion: 1,
    pass: true,
    sourceCommit,
    runtime: {
      nodeVersion: runtime.expectedNodeVersion,
      nodeMajor: Number.parseInt(runtime.expectedNodeVersion.split('.')[0], 10),
      execPath: runtime.expectedExecPath,
      node24Deviation: Number.parseInt(runtime.expectedNodeVersion.split('.')[0], 10) === 24 ? null : `Rebaselined on Node ${runtime.expectedNodeVersion}; not represented as Node 24.`,
    },
    limitBytesExclusive: LIMIT_BYTES_EXCLUSIVE,
    inputManifestHash,
    outputHashes,
    isolated: {
      runs: 5,
      worstDeltaRss: Math.max(...isolatedRows.map(({ deltaRss }) => deltaRss)),
      totalDeltaRss: isolatedRows.reduce((total, { deltaRss }) => total + deltaRss, 0),
    },
    concurrent: {
      concurrency: 2,
      deltaRss: concurrentRow.deltaRss,
      perSearchDeltaRss: concurrentRow.perSearchDeltaRss,
    },
  };
}
