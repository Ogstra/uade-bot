import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { readFile, realpath } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import { SearchOutcomeSchema } from '../src/schemas.js';

const LIMIT_BYTES_EXCLUSIVE = 10 * 1024 * 1024;
const REPO_ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const MANIFEST_FILE = path.join(REPO_ROOT, '.planning/phases/03.2-motor-http-sin-navegador/evidence/webforms-capture-sanitized/manifest.json');
const EXPECTED_FIXTURES = [
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
];
const REQUIRED_CHECKPOINT_STAGES = [
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
];
const ALLOWED_CHECKPOINT_STAGES = new Set([
  ...REQUIRED_CHECKPOINT_STAGES,
  'interval',
  'materia_catalog_response_accumulated',
  'materia_catalog_payload_parse_complete',
  'materia_catalog_body_parse_complete',
]);

function sha256(value) {
  return createHash('sha256').update(value).digest('hex');
}

function parseJson(bytes, label) {
  try { return JSON.parse(bytes.toString('utf8')); } catch { throw new Error(`${label} is not valid JSON`); }
}

function parseJsonl(bytes, label) {
  const lines = bytes.toString('utf8').trim().split(/\r?\n/).filter(Boolean);
  assert(lines.length > 0, `${label} is empty`);
  return lines.map((line, index) => {
    try { return JSON.parse(line); } catch { throw new Error(`${label} line ${index + 1} is not valid JSON`); }
  });
}

function canonical(value) {
  assert.equal(typeof value, 'string', 'runtime execPath must be a string');
  assert(path.isAbsolute(value), 'runtime execPath must be absolute');
  const normalized = path.normalize(value);
  return process.platform === 'win32' ? normalized.toLowerCase() : normalized;
}

function validateCanonicalOutcome(outcome, label) {
  const parsed = SearchOutcomeSchema.safeParse(outcome);
  assert(parsed.success, `${label} does not match SearchOutcomeSchema`);
  assert.deepEqual(parsed.data, outcome, `${label} contains fields outside SearchOutcomeSchema`);
  assert.equal(parsed.data.outcome, 'found', `${label} must be found for the retained benchmark fixture`);
  assert(parsed.data.vacancies.length > 0, `${label} found outcome must contain vacancies`);
}

export function validateWorker(row, runtime, concurrency) {
  assert.equal(row.kind, 'worker-result', 'row is not a worker result');
  assert.equal(row.nodeVersion, runtime.nodeVersion, 'worker runtime version differs from summary');
  assert.equal(canonical(row.execPath), canonical(runtime.execPath), 'worker execPath differs from summary');
  assert.equal(row.concurrency, concurrency, `worker concurrency must be ${concurrency}`);
  assert.equal(row.limitBytesExclusive, LIMIT_BYTES_EXCLUSIVE, 'worker threshold differs from exclusive 10 MiB');
  for (const field of ['baselineRss', 'peakRss', 'deltaRss', 'perSearchDeltaRss']) {
    assert(Number.isSafeInteger(row[field]) && row[field] >= 0, `worker ${field} must be a non-negative safe integer`);
  }
  assert(row.peakRss >= row.baselineRss, 'worker peakRss is below baselineRss');
  assert(row.checkpointRss && typeof row.checkpointRss === 'object' && !Array.isArray(row.checkpointRss), 'worker checkpointRss must be an object');
  const checkpointEntries = Object.entries(row.checkpointRss);
  assert(checkpointEntries.length > 0, 'worker checkpointRss must not be empty');
  for (const stage of REQUIRED_CHECKPOINT_STAGES) {
    assert(Object.hasOwn(row.checkpointRss, stage), `worker checkpointRss is missing ${stage}`);
  }
  for (const [stage, rss] of checkpointEntries) {
    assert(ALLOWED_CHECKPOINT_STAGES.has(stage), `worker checkpointRss contains unknown stage ${stage}`);
    assert(Number.isSafeInteger(rss) && rss >= 0, `worker checkpointRss.${stage} must be a non-negative safe integer`);
    assert(rss <= row.peakRss, `worker checkpointRss.${stage} exceeds peakRss`);
  }
  assert.equal(row.peakRss, Math.max(row.baselineRss, ...checkpointEntries.map(([, rss]) => rss)), 'worker peakRss differs from maximum retained RSS measurement');
  const measuredDeltaRss = row.peakRss - row.baselineRss;
  assert.equal(row.deltaRss, measuredDeltaRss, 'worker deltaRss differs from peakRss - baselineRss');
  const measuredPerSearchDeltaRss = measuredDeltaRss / concurrency;
  assert(Number.isSafeInteger(measuredPerSearchDeltaRss), 'worker per-search delta is not an exact safe integer');
  assert.equal(row.perSearchDeltaRss, measuredPerSearchDeltaRss, 'worker per-search delta differs from deltaRss / concurrency');
  assert(measuredPerSearchDeltaRss < LIMIT_BYTES_EXCLUSIVE, 'worker reached the exclusive 10 MiB threshold');
  assert(Array.isArray(row.outcomes) && row.outcomes.length === concurrency, 'worker outcomes do not match concurrency');
  row.outcomes.forEach((outcome, index) => validateCanonicalOutcome(outcome, `worker outcomes[${index}]`));
  assert(/^[a-f0-9]{64}$/.test(row.outcomeHash), 'worker outcome hash is invalid');
  assert.equal(row.outcomeHash, sha256(JSON.stringify(row.outcomes)), 'worker outcome hash differs from canonical outcomes');
  assert(/^[a-f0-9]{64}$/.test(row.inputManifestHash), 'worker input manifest hash is invalid');
}

export function validateManifestShape(manifest) {
  assert.equal(manifest.schemaVersion, 1, 'fixture schemaVersion differs');
  assert.equal(manifest.baselineKind, 'rebaseline-retained-contract', 'fixture baselineKind differs');
  assert.equal(manifest.syntheticEnvelope, true, 'fixture syntheticEnvelope differs');
  assert.equal(manifest.postbackModeAccepted, 'accepted', 'fixture postback mode differs');
  assert.deepEqual(manifest.fixtures?.map(({ path: fixturePath }) => fixturePath), EXPECTED_FIXTURES, 'fixture allowlist differs');
  assert.equal(manifest.responses?.initialGet?.bytes, 87340, 'initial envelope differs');
  assert.equal(manifest.responses?.fullPostback?.bytes, 87340, 'postback envelope differs');
  assert.equal(manifest.responses?.asyncPostback?.bytes, 87340, 'async postback envelope differs');
  assert.equal(manifest.derivedLimits?.measuredMaxBodyBytes, 87340, 'measured envelope differs');
  assert.equal(manifest.derivedLimits?.measuredDeltaChars, 80154, 'measured delta chars differs');
  assert.equal(manifest.derivedLimits?.measuredDeltaNodes, 17, 'measured delta nodes differs');
  assert.equal(manifest.derivedLimits?.maxBodyBytes, 300000, 'independent body cap differs');
  assert.equal(manifest.derivedLimits?.maxDeltaChars, 100193, 'delta char cap differs');
  assert.equal(manifest.derivedLimits?.maxDeltaNodes, 22, 'delta node cap differs');
}

async function validateManifest(manifestBytes) {
  const manifest = parseJson(manifestBytes, 'fixture manifest');
  validateManifestShape(manifest);
  for (const fixture of manifest.fixtures) {
    const bytes = await readFile(path.join(REPO_ROOT, fixture.path));
    assert.equal(bytes.byteLength, fixture.bytes, `${fixture.path} byte count differs`);
    assert.equal(sha256(bytes), fixture.sha256, `${fixture.path} hash differs`);
  }
  return sha256(manifestBytes);
}

async function main() {
  const evidenceArg = process.argv[2];
  assert(evidenceArg, 'usage: node scripts/validate-http-search-memory-evidence.js <evidence-dir>');
  const evidenceDir = await realpath(path.resolve(evidenceArg));
  const [isolatedBytes, concurrentBytes, summaryBytes, manifestBytes] = await Promise.all([
    readFile(path.join(evidenceDir, 'isolated.jsonl')),
    readFile(path.join(evidenceDir, 'concurrent.jsonl')),
    readFile(path.join(evidenceDir, 'summary.json')),
    readFile(MANIFEST_FILE),
  ]);
  const isolatedRows = parseJsonl(isolatedBytes, 'isolated.jsonl');
  const concurrentRows = parseJsonl(concurrentBytes, 'concurrent.jsonl');
  const summary = parseJson(summaryBytes, 'summary.json');
  assert.equal(summary.pass, true, 'summary pass must be true');
  assert.equal(summary.limitBytesExclusive, LIMIT_BYTES_EXCLUSIVE, 'summary threshold differs');
  const workers = isolatedRows.filter(({ scenario }) => scenario === 'isolated');
  const isolatedSummaryRows = isolatedRows.filter(({ scenario }) => scenario === 'isolated-summary');
  assert.equal(workers.length, 5, 'isolated evidence must contain exactly five workers');
  assert.equal(isolatedSummaryRows.length, 1, 'isolated evidence must contain exactly one summary');
  assert.equal(isolatedRows.length, 6, 'isolated evidence contains unexpected rows');
  assert.deepEqual(workers.map(({ run }) => run), [1, 2, 3, 4, 5], 'isolated runs must be 1..5');
  assert.equal(concurrentRows.length, 1, 'concurrent evidence must contain exactly one row');
  workers.forEach((row) => validateWorker(row, summary.runtime, 1));
  validateWorker(concurrentRows[0], summary.runtime, 2);
  assert(workers.every(({ outcomeHash }) => outcomeHash === workers[0].outcomeHash), 'isolated outcome hashes differ');
  const canonicalOutcome = workers[0].outcomes[0];
  assert(workers.every(({ outcomes }) => JSON.stringify(outcomes[0]) === JSON.stringify(canonicalOutcome)), 'isolated functional outcomes differ');
  assert(concurrentRows[0].outcomes.every((outcome) => JSON.stringify(outcome) === JSON.stringify(canonicalOutcome)), 'concurrent functional outcomes differ from isolated outcome');
  const isolatedSummary = isolatedSummaryRows[0];
  assert.equal(isolatedSummary.status, 'pass', 'isolated summary did not pass');
  assert.equal(isolatedSummary.nodeVersion, summary.runtime.nodeVersion, 'isolated summary runtime differs');
  assert.equal(canonical(isolatedSummary.execPath), canonical(summary.runtime.execPath), 'isolated summary execPath differs');
  assert.equal(isolatedSummary.runs, 5, 'isolated summary run count differs');
  assert.equal(isolatedSummary.limitBytesExclusive, LIMIT_BYTES_EXCLUSIVE, 'isolated summary threshold differs');
  assert.equal(concurrentRows[0].status, 'pass', 'concurrent scenario did not pass');
  const worstDeltaRss = Math.max(...workers.map(({ deltaRss }) => deltaRss));
  const totalDeltaRss = workers.reduce((total, { deltaRss }) => total + deltaRss, 0);
  assert.equal(isolatedSummary.worstDeltaRss, worstDeltaRss, 'isolated JSONL maximum differs');
  assert.equal(summary.isolated?.worstDeltaRss, worstDeltaRss, 'summary isolated maximum differs');
  assert.equal(summary.isolated?.totalDeltaRss, totalDeltaRss, 'summary isolated total differs');
  assert.equal(summary.isolated?.runs, 5, 'summary isolated run count differs');
  assert.equal(summary.concurrent?.concurrency, 2, 'summary concurrent concurrency differs');
  assert.equal(summary.concurrent?.deltaRss, concurrentRows[0].deltaRss, 'summary concurrent delta differs');
  assert.equal(summary.concurrent?.perSearchDeltaRss, concurrentRows[0].perSearchDeltaRss, 'summary concurrent per-search delta differs');
  assert.equal(summary.outputHashes?.isolatedJsonl, sha256(isolatedBytes), 'isolated output hash differs');
  assert.equal(summary.outputHashes?.concurrentJsonl, sha256(concurrentBytes), 'concurrent output hash differs');
  const manifestHash = await validateManifest(manifestBytes);
  assert.equal(summary.inputManifestHash, manifestHash, 'summary input manifest hash differs');
  assert(workers.every(({ inputManifestHash }) => inputManifestHash === manifestHash), 'isolated input manifest hash differs');
  assert.equal(concurrentRows[0].inputManifestHash, manifestHash, 'concurrent input manifest hash differs');
  const head = execFileSync('git', ['-c', `safe.directory=${REPO_ROOT.replaceAll('\\', '/')}`, '-C', REPO_ROOT, 'rev-parse', 'HEAD'], { encoding: 'utf8' }).trim();
  assert.equal(summary.sourceCommit, head, 'summary sourceCommit differs from HEAD');
  assert.equal(summary.runtime.nodeMajor, Number.parseInt(summary.runtime.nodeVersion.split('.')[0], 10), 'runtime major differs');
  assert(summary.runtime.nodeMajor >= 24, 'runtime major is below 24');
  if (summary.runtime.nodeMajor !== 24) {
    assert.match(summary.runtime.node24Deviation ?? '', /not represented as Node 24/, 'runtime deviation is not explicit');
  }
  console.log(`memory-evidence-valid source=${head} runtime=${summary.runtime.nodeVersion} isolatedMax=${worstDeltaRss} concurrentPerSearch=${concurrentRows[0].perSearchDeltaRss}`);
}

const invokedPath = process.argv[1] ? path.resolve(process.argv[1]) : null;
if (invokedPath === fileURLToPath(import.meta.url)) {
  try {
    await main();
  } catch (error) {
    console.error(`memory_evidence_invalid: ${error instanceof Error ? error.message : 'unknown error'}`);
    process.exitCode = 1;
  }
}
