import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import path from 'node:path';

import { SearchOutcomeSchema } from '../src/schemas.js';

export const LIMIT_BYTES_EXCLUSIVE = 10 * 1024 * 1024;
export const MEMORY_SOURCE_SCOPE = Object.freeze([
  'package.json',
  'package-lock.json',
  'scripts/benchmark-http-search-memory.js',
  'scripts/http-search-memory-contract.js',
  'scripts/run-http-search-memory-benchmark.ps1',
  'scripts/validate-http-search-memory-evidence.js',
  ':(glob)src/automation/**/*.js',
  'src/schemas.js',
  'src/logger.js',
]);
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
export const REQUIRED_CHECKPOINT_STAGES = Object.freeze([
  'initial_response_accumulated',
  'initial_turno_parse_complete',
  'search_form_parse_complete',
  'post_response_accumulated',
  'post_body_parse_complete',
  'reflected_state_parse_complete',
  'vacancy_dom_parse_complete',
  'vacancy_row_validation_complete',
  'vacancy_filter_complete',
  'classification_complete',
  'search_chain_complete',
]);
const ALLOWED_CHECKPOINT_STAGES = new Set([
  ...REQUIRED_CHECKPOINT_STAGES,
  'interval',
  'materia_catalog_response_accumulated',
  'materia_catalog_payload_parse_complete',
  'materia_catalog_body_parse_complete',
]);
const EXPECTED_RESPONSES = Object.freeze({ initialGet: 87340, fullPostback: 87340, asyncPostback: 87340 });
const EXPECTED_DERIVED_LIMITS = Object.freeze({
  measuredMaxBodyBytes: 87340,
  measuredDeltaChars: 80154,
  measuredDeltaNodes: 17,
  maxBodyBytes: 300000,
  maxDeltaChars: 100193,
  maxDeltaNodes: 22,
});

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
  for (const [response, bytes] of Object.entries(EXPECTED_RESPONSES)) {
    assert.equal(manifest?.responses?.[response]?.bytes, bytes, `${response} envelope must be ${bytes} bytes`);
  }
  for (const [limit, value] of Object.entries(EXPECTED_DERIVED_LIMITS)) {
    assert.equal(manifest?.derivedLimits?.[limit], value, `${limit} must be ${value}`);
  }
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
    envelopeBytes: EXPECTED_RESPONSES.initialGet,
    maxBodyBytes: EXPECTED_DERIVED_LIMITS.maxBodyBytes,
    maxDeltaChars: EXPECTED_DERIVED_LIMITS.maxDeltaChars,
    maxDeltaNodes: EXPECTED_DERIVED_LIMITS.maxDeltaNodes,
  };
}

function outcomeHash(outcomes) {
  return createHash('sha256').update(JSON.stringify(outcomes)).digest('hex');
}

function validateCanonicalOutcome(outcome, label) {
  const parsed = SearchOutcomeSchema.safeParse(outcome);
  assert(parsed.success, `${label} does not match SearchOutcomeSchema`);
  assert.deepEqual(parsed.data, outcome, `${label} contains fields outside SearchOutcomeSchema`);
  assert.equal(parsed.data.outcome, 'found', `${label} must be found for the retained benchmark fixture`);
  assert(parsed.data.vacancies.length > 0, `${label} found outcome must contain vacancies`);
  return parsed.data;
}

function validateCheckpointRss(checkpointRss, baselineRss, peakRss) {
  assert(checkpointRss && typeof checkpointRss === 'object' && !Array.isArray(checkpointRss), 'worker checkpointRss must be an object');
  const entries = Object.entries(checkpointRss);
  assert(entries.length > 0, 'worker checkpointRss must not be empty');
  for (const stage of REQUIRED_CHECKPOINT_STAGES) {
    assert(Object.hasOwn(checkpointRss, stage), `worker checkpointRss is missing ${stage}`);
  }
  for (const [stage, rss] of entries) {
    assert(ALLOWED_CHECKPOINT_STAGES.has(stage), `worker checkpointRss contains unknown stage ${stage}`);
    assert(Number.isSafeInteger(rss) && rss >= 0, `worker checkpointRss.${stage} must be a non-negative safe integer`);
    assert(rss <= peakRss, `worker checkpointRss.${stage} exceeds peakRss`);
  }
  assert.equal(peakRss, Math.max(baselineRss, ...entries.map(([, rss]) => rss)), 'worker peakRss must equal the maximum retained RSS measurement');
}

export function validateWorkerResult(result, { expectedExecPath, expectedNodeVersion }) {
  assert.equal(result?.kind, 'worker-result', 'worker kind must be worker-result');
  assert.equal(result.nodeVersion, expectedNodeVersion, 'worker nodeVersion differs from parent');
  assert.equal(canonicalPath(result.execPath), canonicalPath(expectedExecPath), 'worker execPath differs from approved execPath');
  assert([1, 2].includes(result.concurrency), 'worker concurrency must be one or two');
  assert.equal(result.limitBytesExclusive, LIMIT_BYTES_EXCLUSIVE, 'worker memory limit changed');
  for (const field of ['baselineRss', 'peakRss', 'deltaRss', 'perSearchDeltaRss']) {
    assert(Number.isSafeInteger(result[field]) && result[field] >= 0, `worker ${field} must be a non-negative safe integer`);
  }
  assert(result.peakRss >= result.baselineRss, 'worker peakRss must not be below baselineRss');
  validateCheckpointRss(result.checkpointRss, result.baselineRss, result.peakRss);
  const measuredDeltaRss = result.peakRss - result.baselineRss;
  assert.equal(result.deltaRss, measuredDeltaRss, 'worker deltaRss must equal peakRss - baselineRss');
  const measuredPerSearchDeltaRss = measuredDeltaRss / result.concurrency;
  assert(Number.isSafeInteger(measuredPerSearchDeltaRss), 'worker per-search RSS delta must be an exact safe integer');
  assert.equal(result.perSearchDeltaRss, measuredPerSearchDeltaRss, 'worker perSearchDeltaRss must equal deltaRss / concurrency');
  assert(measuredPerSearchDeltaRss < LIMIT_BYTES_EXCLUSIVE, 'worker reached the exclusive 10 MiB limit');
  assert(Array.isArray(result.outcomes) && result.outcomes.length === result.concurrency, 'worker outcomes must match concurrency');
  result.outcomes.forEach((outcome, index) => validateCanonicalOutcome(outcome, `worker outcomes[${index}]`));
  assert(HEX_64.test(result.outcomeHash), 'worker outcomeHash is invalid');
  assert.equal(result.outcomeHash, outcomeHash(result.outcomes), 'worker outcomeHash does not match canonical outcomes');
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
  assert(isolatedRows.every((row) => row.outcomeHash === isolatedRows[0].outcomeHash), 'isolated outcome hashes differ');
  const canonicalOutcome = isolatedRows[0].outcomes[0];
  assert(isolatedRows.every((row) => JSON.stringify(row.outcomes[0]) === JSON.stringify(canonicalOutcome)), 'isolated functional outcomes differ');
  assert(concurrentRow.outcomes.every((outcome) => JSON.stringify(outcome) === JSON.stringify(canonicalOutcome)), 'concurrent functional outcomes differ from isolated outcome');
  return {
    schemaVersion: 1,
    pass: true,
    sourceCommit,
    sourceScope: [...MEMORY_SOURCE_SCOPE],
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
