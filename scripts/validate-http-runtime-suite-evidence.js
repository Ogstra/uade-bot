import { createHash } from 'node:crypto';
import { execFileSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const REPO_ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');

export const APPROVED_TEST_FILES = Object.freeze([
  'src/automation/http-session.test.js',
  'src/automation/http-search.test.js',
  'src/automation/webforms.test.js',
  'src/automation/delta-response.test.js',
  'src/automation/parse-results.test.js',
  'src/scheduler/poller.test.js',
  'src/scheduler/queue.test.js',
  'src/discord/credentials-flow.test.js',
]);
export const APPROVED_SOURCE_SCOPE = Object.freeze([
  'package.json',
  'package-lock.json',
  ':(glob)scripts/*.js',
  'scripts/run-http-search-memory-benchmark.ps1',
  ':(glob)src/**/*.js',
]);

export function buildCanonicalTestCommand(testFiles = APPROVED_TEST_FILES) {
  return `node --test --test-reporter=tap ${testFiles.join(' ')}`;
}

function sha256(value) {
  return createHash('sha256').update(value).digest('hex');
}

function fail(message) {
  throw new Error(`runtime evidence validation failed: ${message}`);
}

function requireInteger(value, label) {
  if (!Number.isSafeInteger(value) || value < 0) {
    fail(`${label} must be a non-negative integer`);
  }
}

function parseTerminalFooter(tap) {
  const footerPattern = /(?:^|\r?\n)1\.\.(\d+)\r?\n# tests (\d+)\r?\n# suites (\d+)\r?\n# pass (\d+)\r?\n# fail (\d+)\r?\n# cancelled (\d+)\r?\n# skipped (\d+)\r?\n# todo (\d+)\r?\n# duration_ms ([0-9]+(?:\.[0-9]+)?)/g;
  const matches = [...tap.matchAll(footerPattern)];
  if (matches.length !== 1) {
    fail(`expected exactly one complete TAP footer, found ${matches.length}`);
  }

  const match = matches[0];
  const trailing = tap.slice(match.index + match[0].length);
  if (!/^\s*$/.test(trailing)) {
    fail('TAP footer must be terminal; only whitespace is allowed after duration_ms');
  }

  const [plan, tests, suites, pass, failures, cancelled, skipped, todo] = match
    .slice(1, 9)
    .map(Number);
  const resultLines = [...tap.matchAll(/^([ \t]*)(not ok|ok) (\d+)(?:[ \t]+-[^\r\n]*)?$/gm)];
  const failedResult = resultLines.find(([, , status]) => status === 'not ok');
  if (failedResult) {
    fail(`TAP contains a failing test point: ${failedResult[0].trim()}`);
  }
  const directedResult = resultLines.find((matchResult) => /\s+#\s*(?:SKIP|TODO)(?:\s|$)/i.test(matchResult[0]));
  if (directedResult) {
    fail(`TAP contains a skipped or todo test point: ${directedResult[0].trim()}`);
  }
  const topLevelResults = resultLines.filter(([, indentation]) => indentation.length === 0).length;
  if (plan !== topLevelResults) {
    fail(`TAP plan 1..${plan} does not match ${topLevelResults} top-level results`);
  }
  if (tests !== pass + failures + cancelled + skipped + todo) {
    fail('TAP count arithmetic does not reconcile');
  }
  if (failures !== 0) {
    fail(`TAP fail count must be zero, received ${failures}`);
  }
  if (cancelled !== 0) {
    fail(`TAP cancelled count must be zero, received ${cancelled}`);
  }
  if (skipped !== 0) {
    fail(`TAP skipped count must be zero, received ${skipped}`);
  }
  if (todo !== 0) {
    fail(`TAP todo count must be zero, received ${todo}`);
  }
  if (pass !== tests) {
    fail(`TAP pass count ${pass} must equal tests ${tests}`);
  }

  return { tests, suites, pass, fail: failures, cancelled, skipped, todo };
}

function validateManifestCounts(manifestCounts, tapCounts) {
  if (!manifestCounts || typeof manifestCounts !== 'object') {
    fail('manifest counts are required');
  }
  for (const field of ['tests', 'pass', 'fail', 'skipped', 'todo']) {
    requireInteger(manifestCounts[field], `manifest counts.${field}`);
    if (manifestCounts[field] !== tapCounts[field]) {
      fail(`manifest counts.${field} does not match TAP ${field}`);
    }
  }
}

function validatePackageFiles(packageFiles) {
  const expectedPaths = ['package.json', 'package-lock.json'];
  if (!Array.isArray(packageFiles) || packageFiles.length !== expectedPaths.length) {
    fail('packageFiles must contain package.json and package-lock.json');
  }
  for (let index = 0; index < expectedPaths.length; index += 1) {
    const record = packageFiles[index];
    const expectedPath = expectedPaths[index];
    if (record?.path !== expectedPath) {
      fail(`packageFiles[${index}].path must be ${expectedPath}`);
    }
    if (record.beforeSha256 !== record.afterSha256) {
      fail(`${expectedPath} hash changed across npm ci`);
    }
    const currentHash = sha256(readFileSync(path.join(REPO_ROOT, expectedPath)));
    if (record.afterSha256 !== currentHash) {
      fail(`${expectedPath} hash does not match the current file`);
    }
  }
}

function gitOutput(args) {
  return execFileSync('git', [
    '-c', `safe.directory=${REPO_ROOT.replaceAll('\\', '/')}`,
    '-C', REPO_ROOT,
    ...args,
  ], {
    encoding: 'utf8',
    stdio: ['ignore', 'pipe', 'pipe'],
  }).trim();
}

export function validateScopedGitStatus(status) {
  if (typeof status !== 'string') {
    fail('scoped Git status must be a string');
  }
  if (status.trim() !== '') {
    fail(`source scope differs from HEAD: ${status.trim().split(/\r?\n/)[0]}`);
  }
}

function validateSourceCommit(commit, scopedGitStatus) {
  if (typeof commit !== 'string' || !/^[0-9a-f]{40}$/.test(commit)) {
    fail('manifest commit must be a full lowercase Git object id');
  }
  let head;
  try {
    head = gitOutput(['rev-parse', 'HEAD']);
  } catch {
    fail('manifest commit could not be compared with HEAD');
  }
  if (commit !== head) {
    fail('manifest commit must exactly match HEAD');
  }
  let scopedStatus = scopedGitStatus;
  if (scopedStatus === undefined) {
    try {
      scopedStatus = gitOutput(['status', '--porcelain=v1', '--untracked-files=all', '--', ...APPROVED_SOURCE_SCOPE]);
    } catch {
      fail('source scope could not be compared with HEAD');
    }
  }
  validateScopedGitStatus(scopedStatus);
}

export function validateEvidenceDirectory(evidenceDirectory, { scopedGitStatus } = {}) {
  const manifestPath = path.join(evidenceDirectory, 'manifest.json');
  const manifest = JSON.parse(readFileSync(manifestPath, 'utf8'));
  if (manifest.schemaVersion !== 1) {
    fail('schemaVersion must be 1');
  }
  if (manifest.evidenceFile !== 'targeted-suite.tap') {
    fail('evidenceFile must be targeted-suite.tap');
  }

  const tapBytes = readFileSync(path.join(evidenceDirectory, manifest.evidenceFile));
  if (manifest.evidenceSha256 !== sha256(tapBytes)) {
    fail('targeted-suite.tap hash does not match manifest');
  }
  const tapCounts = parseTerminalFooter(tapBytes.toString('utf8'));
  validateManifestCounts(manifest.counts, tapCounts);

  if (JSON.stringify(manifest.testFiles) !== JSON.stringify(APPROVED_TEST_FILES)) {
    fail('manifest testFiles must exactly match the approved ordered allowlist');
  }
  if (manifest.testCommand !== buildCanonicalTestCommand()) {
    fail('manifest test command does not match the approved allowlist');
  }
  if (JSON.stringify(manifest.sourceScope) !== JSON.stringify(APPROVED_SOURCE_SCOPE)) {
    fail('manifest sourceScope must exactly match the approved ordered scope');
  }
  validatePackageFiles(manifest.packageFiles);

  if (manifest.runtime?.version !== process.version) {
    fail(`runtime version does not match current runtime ${process.version}`);
  }
  if (manifest.runtime?.execPath !== process.execPath) {
    fail(`runtime execPath does not match current execPath ${process.execPath}`);
  }
  validateSourceCommit(manifest.commit, scopedGitStatus);

  return { manifest, counts: tapCounts };
}

const invokedPath = process.argv[1] ? path.resolve(process.argv[1]) : null;
if (invokedPath === fileURLToPath(import.meta.url)) {
  const evidenceDirectory = process.argv[2];
  if (!evidenceDirectory) {
    console.error('Usage: node scripts/validate-http-runtime-suite-evidence.js <evidence-directory>');
    process.exitCode = 1;
  } else {
    try {
      const result = validateEvidenceDirectory(evidenceDirectory);
      console.log(`runtime suite evidence valid: ${result.counts.tests} tests, ${result.counts.fail} failures`);
    } catch (error) {
      console.error(error.message);
      process.exitCode = 1;
    }
  }
}
