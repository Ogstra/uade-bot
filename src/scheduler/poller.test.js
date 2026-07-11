import { test, after } from 'node:test';
import assert from 'node:assert/strict';
import { randomBytes } from 'node:crypto';
import { createDatabase } from '../db/database.js';
import { upsertUser, getUser } from '../db/users.repository.js';
import { upsertCredentials } from '../db/credentials.repository.js';
import { createJob, getJob } from '../db/jobs.repository.js';
import { encryptCredentials } from '../crypto/credentials-crypto.js';
import { getBrowser } from '../automation/browser.js';
import { pollOnce } from './poller.js';

// A 'verified' outcome flows through parseResults(), which launches (and
// reuses) the shared headless Chromium instance — same reason as
// src/automation/parse-results.test.js's after() hook: without an explicit
// close, that Chromium process outlives this test run and keeps
// `node --test` from exiting on its own.
after(async () => {
  const browser = await getBrowser();
  await browser.close();
});

// pollOnce internally calls loadEnv() to read CREDENTIALS_MASTER_KEY (and,
// via the EnvSchema, UADE_USERNAME/UADE_PASSWORD are still required fields).
// These are set here, BEFORE any loadEnv() call, so this test is
// deterministic regardless of the developer's local .env contents — dotenv's
// config() never overrides a process.env value that's already set.
process.env.UADE_USERNAME = process.env.UADE_USERNAME || 'test-username-placeholder';
process.env.UADE_PASSWORD = process.env.UADE_PASSWORD || 'test-password-placeholder';
const MASTER_KEY = randomBytes(32).toString('hex');
process.env.CREDENTIALS_MASTER_KEY = MASTER_KEY;

const FILTROS = {
  materiaCodigo: '3.1.050',
  ofrecimiento: 'curricular',
  turno: 'Mañana',
  dias: ['LU'],
  sedesExcluidas: [],
};

const PLAINTEXT = {
  uadeUsername: 'realUadeUser',
  uadePassword: 'sup3rSecretPass!',
  uadeStartUrl: 'https://inscripcionespia.uade.edu.ar/x?param=abc123secret',
};

function seedJob(db, discordUserId = 'user-1') {
  upsertUser(db, discordUserId);
  const cipher = encryptCredentials(MASTER_KEY, discordUserId, PLAINTEXT);
  upsertCredentials(db, { discordUserId, ...cipher });
  return createJob(db, { discordUserId, filtros: FILTROS });
}

test('pollOnce persists a no_vacancies outcome for a verified search with zero parsed vacancies', async () => {
  const db = createDatabase(':memory:');
  try {
    const job = seedJob(db);
    const before = Date.now();

    const runSearchFn = async () => ({ status: 'verified', html: '<table></table>' });
    const withUadeContextFn = async (creds, run) => run({});

    await pollOnce(db, job.id, { withUadeContextFn, runSearchFn });

    const updated = getJob(db, job.id);
    assert.equal(updated.lastOutcome, JSON.stringify({ outcome: 'no_vacancies' }));
    assert.ok(updated.lastPolledAt >= before);
  } finally {
    db.close();
  }
});

test('pollOnce persists an invalid_credentials outcome', async () => {
  const db = createDatabase(':memory:');
  try {
    const job = seedJob(db);

    const runSearchFn = async () => ({ status: 'invalid_credentials' });
    const withUadeContextFn = async (creds, run) => run({});

    await pollOnce(db, job.id, { withUadeContextFn, runSearchFn });

    const updated = getJob(db, job.id);
    assert.equal(updated.lastOutcome, JSON.stringify({ outcome: 'invalid_credentials' }));
  } finally {
    db.close();
  }
});

test('pollOnce never leaves plaintext credentials in any DB table after resolving', async () => {
  const db = createDatabase(':memory:');
  try {
    const job = seedJob(db);

    const runSearchFn = async () => ({ status: 'verified', html: '<table></table>' });
    const withUadeContextFn = async (creds, run) => run({});

    await pollOnce(db, job.id, { withUadeContextFn, runSearchFn });

    const allRows = [
      ...db.prepare('SELECT * FROM users').all(),
      ...db.prepare('SELECT * FROM credentials').all(),
      ...db.prepare('SELECT * FROM jobs').all(),
    ];
    const allRowsText = JSON.stringify(allRows);

    assert.equal(allRowsText.includes(PLAINTEXT.uadePassword), false);
    assert.equal(allRowsText.includes(PLAINTEXT.uadeStartUrl), false);
  } finally {
    db.close();
  }
});

test('pollOnce decrypts credentials transiently and passes them to withUadeContextFn/runSearchFn', async () => {
  const db = createDatabase(':memory:');
  try {
    const job = seedJob(db);

    let capturedCreds;
    let capturedSearchArgs;

    const withUadeContextFn = async (creds, run) => {
      capturedCreds = creds;
      return run({});
    };
    const runSearchFn = async (context, filtros, options) => {
      capturedSearchArgs = { filtros, options };
      return { status: 'verified', html: '<table></table>' };
    };

    await pollOnce(db, job.id, { withUadeContextFn, runSearchFn });

    assert.deepEqual(capturedCreds, {
      username: PLAINTEXT.uadeUsername,
      password: PLAINTEXT.uadePassword,
    });
    assert.deepEqual(capturedSearchArgs.options, { startUrl: PLAINTEXT.uadeStartUrl });
  } finally {
    db.close();
  }
});

test('pollOnce persists needs_credentials account-level pause state after an invalid_credentials poll result (D-04)', async () => {
  const db = createDatabase(':memory:');
  try {
    const job = seedJob(db);

    const before = getUser(db, job.discordUserId);
    assert.equal(before.pauseReason, null);

    const runSearchFn = async () => ({ status: 'invalid_credentials' });
    const withUadeContextFn = async (creds, run) => run({});

    await pollOnce(db, job.id, { withUadeContextFn, runSearchFn });

    const after = getUser(db, job.discordUserId);
    assert.equal(after.pauseReason, 'needs_credentials');
  } finally {
    db.close();
  }
});
