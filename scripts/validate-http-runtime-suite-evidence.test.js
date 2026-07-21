import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { execFileSync } from 'node:child_process';
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

import {
  APPROVED_TEST_FILES,
  buildCanonicalTestCommand,
  validateEvidenceDirectory,
} from './validate-http-runtime-suite-evidence.js';

const sha256 = (value) => createHash('sha256').update(value).digest('hex');
const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const gitArgs = ['-c', `safe.directory=${repoRoot.replaceAll('\\', '/')}`, '-C', repoRoot];
const head = execFileSync('git', [...gitArgs, 'rev-parse', 'HEAD'], {
  encoding: 'utf8',
}).trim();
const parentCommit = execFileSync('git', [...gitArgs, 'rev-parse', 'HEAD^'], {
  encoding: 'utf8',
}).trim();

function completeTap(overrides = {}) {
  const counts = {
    tests: 3,
    suites: 0,
    pass: 3,
    fail: 0,
    cancelled: 0,
    skipped: 0,
    todo: 0,
    ...overrides,
  };
  return [
    'TAP version 13',
    'ok 1 - first',
    'ok 2 - second',
    'ok 3 - third',
    `1..${counts.tests}`,
    `# tests ${counts.tests}`,
    `# suites ${counts.suites}`,
    `# pass ${counts.pass}`,
    `# fail ${counts.fail}`,
    `# cancelled ${counts.cancelled}`,
    `# skipped ${counts.skipped}`,
    `# todo ${counts.todo}`,
    '# duration_ms 12.5',
    '',
  ].join('\n');
}

function currentFileRecord(filePath) {
  const bytes = readFileSync(path.join(repoRoot, filePath));
  const digest = sha256(bytes);
  return { path: filePath, beforeSha256: digest, afterSha256: digest };
}

function makeEvidence({ tap = completeTap(), mutate } = {}) {
  const directory = mkdtempSync(path.join(tmpdir(), 'runtime-suite-evidence-'));
  const manifest = {
    schemaVersion: 1,
    evidenceFile: 'targeted-suite.tap',
    evidenceSha256: sha256(tap),
    testFiles: [...APPROVED_TEST_FILES],
    testCommand: buildCanonicalTestCommand(APPROVED_TEST_FILES),
    counts: { tests: 3, pass: 3, fail: 0, skipped: 0, todo: 0 },
    packageFiles: [currentFileRecord('package.json'), currentFileRecord('package-lock.json')],
    runtime: { version: process.version, execPath: process.execPath },
    commit: head,
  };
  mutate?.(manifest);
  writeFileSync(path.join(directory, 'targeted-suite.tap'), tap);
  writeFileSync(path.join(directory, 'manifest.json'), `${JSON.stringify(manifest, null, 2)}\n`);
  return directory;
}

function rejectsEvidence(options, pattern) {
  const directory = makeEvidence(options);
  try {
    assert.throws(() => validateEvidenceDirectory(directory), pattern);
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
}

test('accepts one complete terminal TAP footer and a matching manifest', () => {
  const directory = makeEvidence();
  try {
    assert.deepEqual(validateEvidenceDirectory(directory).counts, {
      tests: 3,
      suites: 0,
      pass: 3,
      fail: 0,
      cancelled: 0,
      skipped: 0,
      todo: 0,
    });
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});

test('accepts a top-level plan smaller than total tests when suites contain nested tests', () => {
  const tap = completeTap({ suites: 1 }).replace(
    ['ok 1 - first', 'ok 2 - second', 'ok 3 - third', '1..3'].join('\n'),
    ['# Subtest: grouped', '    ok 1 - nested', '    1..1', 'ok 1 - grouped', 'ok 2 - standalone', '1..2'].join('\n'),
  );
  const directory = makeEvidence({ tap });
  try {
    assert.equal(validateEvidenceDirectory(directory).counts.tests, 3);
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});

test('rejects a truncated TAP footer', () => {
  rejectsEvidence({ tap: completeTap().replace(/# duration_ms[^\n]*\n$/, '') }, /footer/i);
});

test('rejects duplicate TAP footers', () => {
  rejectsEvidence({ tap: `${completeTap()}${completeTap()}` }, /footer|terminal/i);
});

test('rejects a valid footer followed by another TAP result', () => {
  rejectsEvidence({ tap: `${completeTap()}ok 4 - late\n` }, /terminal/i);
});

test('rejects a valid footer followed by a truncated fragment', () => {
  rejectsEvidence({ tap: `${completeTap()}# tests` }, /terminal/i);
});

for (const field of ['tests', 'pass', 'fail', 'skipped', 'todo']) {
  test(`rejects falsified manifest count: ${field}`, () => {
    rejectsEvidence({ mutate: (manifest) => { manifest.counts[field] += 1; } }, new RegExp(field, 'i'));
  });
}

test('rejects arithmetic that does not reconcile', () => {
  rejectsEvidence({ tap: completeTap({ pass: 2 }) }, /arithmetic/i);
});

test('rejects a non-zero failure count', () => {
  rejectsEvidence({ tap: completeTap({ pass: 2, fail: 1 }) }, /fail/i);
});

test('rejects a non-zero cancelled count', () => {
  rejectsEvidence({ tap: completeTap({ pass: 2, cancelled: 1 }) }, /cancelled/i);
});

test('rejects a plan that disagrees with the test count', () => {
  rejectsEvidence({ tap: completeTap().replace('1..3', '1..2') }, /plan/i);
});

test('rejects an additional test file', () => {
  rejectsEvidence({ mutate: (manifest) => { manifest.testFiles.push('src/extra.test.js'); } }, /testFiles/i);
});

test('rejects a missing test file', () => {
  rejectsEvidence({ mutate: (manifest) => { manifest.testFiles.pop(); } }, /testFiles/i);
});

test('rejects a changed evidence hash', () => {
  rejectsEvidence({ mutate: (manifest) => { manifest.evidenceSha256 = '0'.repeat(64); } }, /hash/i);
});

test('rejects a changed runtime', () => {
  rejectsEvidence({ mutate: (manifest) => { manifest.runtime.version = 'v0.0.0'; } }, /runtime/i);
});

test('rejects a changed execPath', () => {
  rejectsEvidence({ mutate: (manifest) => { manifest.runtime.execPath = 'C:\\fake\\node.exe'; } }, /execPath/i);
});

test('rejects a changed commit', () => {
  rejectsEvidence({ mutate: (manifest) => { manifest.commit = '0'.repeat(40); } }, /commit/i);
});

test('rejects a real ancestor commit because evidence must certify exact HEAD', () => {
  rejectsEvidence({ mutate: (manifest) => { manifest.commit = parentCommit; } }, /exactly match HEAD/i);
});

test('validates package hashes and Git commit independently of the current working directory', () => {
  const directory = makeEvidence();
  const previousCwd = process.cwd();
  const foreignCwd = mkdtempSync(path.join(tmpdir(), 'runtime-suite-cwd-'));
  try {
    process.chdir(foreignCwd);
    assert.equal(validateEvidenceDirectory(directory).counts.fail, 0);
  } finally {
    process.chdir(previousCwd);
    rmSync(foreignCwd, { recursive: true, force: true });
    rmSync(directory, { recursive: true, force: true });
  }
});

test('rejects a changed command', () => {
  rejectsEvidence({ mutate: (manifest) => { manifest.testCommand += ' src/extra.test.js'; } }, /command/i);
});

test('rejects package hashes changed across npm ci', () => {
  rejectsEvidence({ mutate: (manifest) => { manifest.packageFiles[0].afterSha256 = '0'.repeat(64); } }, /package|hash/i);
});
