import { test, after } from 'node:test';
import assert from 'node:assert/strict';
import { randomBytes } from 'node:crypto';
import { createDatabase } from '../db/database.js';
import { upsertUser, getUser } from '../db/users.repository.js';
import { upsertCredentials } from '../db/credentials.repository.js';
import { createJob, getJob } from '../db/jobs.repository.js';
import { getMateriaNombre } from '../db/materias.repository.js';
import { listHistoryForJob } from '../db/poll-history.repository.js';
import { encryptCredentials } from '../crypto/credentials-crypto.js';
import { getBrowser } from '../automation/browser.js';
import { pollOnce, attemptAutoRelink } from './poller.js';

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
process.env.DISCORD_BOT_TOKEN = process.env.DISCORD_BOT_TOKEN || 'test-discord-token';
process.env.DISCORD_CLIENT_ID = process.env.DISCORD_CLIENT_ID || 'test-discord-client-id';
process.env.DISCORD_GUILD_ID = process.env.DISCORD_GUILD_ID || 'test-discord-guild-id';

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

    const runSearchFn = async () => ({
      status: 'verified',
      html: '<table></table>',
      materiaNombre: 'Fisica II',
    });
    const withUadeContextFn = async (creds, run) => run({});

    await pollOnce(db, job.id, { withUadeContextFn, runSearchFn });

    const updated = getJob(db, job.id);
    assert.equal(updated.lastOutcome, JSON.stringify({ outcome: 'no_vacancies', materiaNombre: 'Fisica II' }));
    assert.ok(updated.lastPolledAt >= before);
    assert.equal(getMateriaNombre(db, FILTROS.materiaCodigo), 'Fisica II');
  } finally {
    db.close();
  }
});

test('pollOnce writes current state and bounded history through one timestamped boundary', async () => {
  const db = createDatabase(':memory:');
  try {
    const job = seedJob(db);
    const withUadeContextFn = async (creds, run) => run({});
    const noVacancies = async () => ({ status: 'verified', html: '<table></table>' });

    await pollOnce(db, job.id, { withUadeContextFn, runSearchFn: noVacancies });
    await pollOnce(db, job.id, { withUadeContextFn, runSearchFn: noVacancies });

    assert.equal(listHistoryForJob(db, job.id).length, 1);

    const invalidCredentials = async () => ({ status: 'invalid_credentials' });
    await pollOnce(db, job.id, { withUadeContextFn, runSearchFn: invalidCredentials });

    const history = listHistoryForJob(db, job.id);
    const updated = getJob(db, job.id);
    assert.deepEqual(history.map((record) => record.outcomeCode), ['invalid_credentials', 'no_vacancies']);
    assert.equal(history[0].recordedAt, updated.lastPolledAt);
  } finally {
    db.close();
  }
});

test('pollOnce does not cache a materia name when the poll result carries none', async () => {
  const db = createDatabase(':memory:');
  try {
    const job = seedJob(db);

    const runSearchFn = async () => ({ status: 'verified', html: '<table></table>' });
    const withUadeContextFn = async (creds, run) => run({});

    await pollOnce(db, job.id, { withUadeContextFn, runSearchFn });

    assert.equal(getMateriaNombre(db, FILTROS.materiaCodigo), null);
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
      ...db.prepare('SELECT * FROM poll_outcome_history').all(),
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

// --- AUTOLINK-03: attemptAutoRelinkFn is only reachable from the
// stale_start_url branch of pollOnce -------------------------------------

test('pollOnce never calls attemptAutoRelinkFn for a verified/no_vacancies outcome', async () => {
  const db = createDatabase(':memory:');
  try {
    const job = seedJob(db);
    let relinkCalls = 0;
    const attemptAutoRelinkFn = async () => {
      relinkCalls += 1;
      return { status: 'fallback' };
    };
    const runSearchFn = async () => ({ status: 'verified', html: '<table></table>' });
    const withUadeContextFn = async (creds, run) => run({});

    await pollOnce(db, job.id, { withUadeContextFn, runSearchFn, attemptAutoRelinkFn });

    assert.equal(relinkCalls, 0);
  } finally {
    db.close();
  }
});

test('pollOnce never calls attemptAutoRelinkFn for an invalid_credentials/rate_limited/search_failed outcome', async () => {
  const db = createDatabase(':memory:');
  try {
    const job = seedJob(db);
    let relinkCalls = 0;
    const attemptAutoRelinkFn = async () => {
      relinkCalls += 1;
      return { status: 'fallback' };
    };
    const withUadeContextFn = async (creds, run) => run({});

    for (const status of ['invalid_credentials', 'rate_limited', 'search_failed']) {
      relinkCalls = 0;
      const runSearchFn = async () => (status === 'search_failed' ? { status, reason: 'postback_timeout' } : { status });
      await pollOnce(db, job.id, { withUadeContextFn, runSearchFn, attemptAutoRelinkFn });
      assert.equal(relinkCalls, 0, `attemptAutoRelinkFn must not be called for outcome "${status}"`);
    }
  } finally {
    db.close();
  }
});

test('pollOnce calls attemptAutoRelinkFn exactly once, with the job and the already-decrypted credentials, when the outcome is stale_start_url', async () => {
  const db = createDatabase(':memory:');
  try {
    const job = seedJob(db);
    let relinkCalls = 0;
    let capturedJob;
    let capturedCreds;
    const attemptAutoRelinkFn = async (dbArg, jobArg, creds) => {
      relinkCalls += 1;
      capturedJob = jobArg;
      capturedCreds = creds;
      return { status: 'fallback' };
    };
    const runSearchFn = async () => ({ status: 'stale_start_url' });
    const withUadeContextFn = async (creds, run) => run({});

    await pollOnce(db, job.id, { withUadeContextFn, runSearchFn, attemptAutoRelinkFn });

    assert.equal(relinkCalls, 1);
    assert.equal(capturedJob.id, job.id);
    assert.deepEqual(capturedCreds, {
      username: PLAINTEXT.uadeUsername,
      password: PLAINTEXT.uadePassword,
      masterKey: MASTER_KEY,
    });
  } finally {
    db.close();
  }
});

test('a successful automatic relink clears the account pause state after a stale_start_url poll (no needs_new_start_url pause, no DM)', async () => {
  const db = createDatabase(':memory:');
  try {
    const job = seedJob(db);
    const attemptAutoRelinkFn = async () => ({ status: 'success' });
    const runSearchFn = async () => ({ status: 'stale_start_url' });
    const withUadeContextFn = async (creds, run) => run({});

    await pollOnce(db, job.id, { withUadeContextFn, runSearchFn, attemptAutoRelinkFn });

    const after = getUser(db, job.discordUserId);
    assert.equal(after.pauseReason, null);
  } finally {
    db.close();
  }
});

test('a fallback automatic relink preserves the exact pre-existing needs_new_start_url pause behavior', async () => {
  const db = createDatabase(':memory:');
  try {
    const job = seedJob(db);
    const attemptAutoRelinkFn = async () => ({ status: 'fallback' });
    const runSearchFn = async () => ({ status: 'stale_start_url' });
    const withUadeContextFn = async (creds, run) => run({});

    await pollOnce(db, job.id, { withUadeContextFn, runSearchFn, attemptAutoRelinkFn });

    const after = getUser(db, job.discordUserId);
    assert.equal(after.pauseReason, 'needs_new_start_url');
  } finally {
    db.close();
  }
});

test('a successful automatic relink never mutates the persisted outcome — lastOutcome still reflects stale_start_url', async () => {
  const db = createDatabase(':memory:');
  try {
    const job = seedJob(db);
    const attemptAutoRelinkFn = async () => ({ status: 'success' });
    const runSearchFn = async () => ({ status: 'stale_start_url' });
    const withUadeContextFn = async (creds, run) => run({});

    await pollOnce(db, job.id, { withUadeContextFn, runSearchFn, attemptAutoRelinkFn });

    const updated = getJob(db, job.id);
    assert.equal(updated.lastOutcome, JSON.stringify({ outcome: 'stale_start_url' }));
  } finally {
    db.close();
  }
});

// --- attemptAutoRelink unit behavior --------------------------------------

test('attemptAutoRelink rotates uadeStartUrl via rotateCredentialValuesFn and returns only { status: "success" } on an obtainStartUrlFn success', async () => {
  const db = createDatabase(':memory:');
  try {
    const job = seedJob(db);
    let rotateArgs;
    const withPlainContextFn = async (run) => run({});
    const obtainStartUrlFn = async () => ({
      status: 'success',
      startUrl: 'https://inscripcionespia.uade.edu.ar/x?param=freshlink123',
    });
    const rotateCredentialValuesFn = (dbArg, args) => {
      rotateArgs = args;
    };

    const result = await attemptAutoRelink(
      db,
      job,
      { username: 'someuser', password: 'somepass', masterKey: MASTER_KEY },
      { withPlainContextFn, obtainStartUrlFn, rotateCredentialValuesFn },
    );

    assert.deepEqual(result, { status: 'success' });
    assert.deepEqual(Object.keys(result), ['status']);
    assert.equal(rotateArgs.discordUserId, job.discordUserId);
    assert.equal(rotateArgs.masterKey, MASTER_KEY);
    assert.deepEqual(rotateArgs.updates, { uadeStartUrl: 'https://inscripcionespia.uade.edu.ar/x?param=freshlink123' });
  } finally {
    db.close();
  }
});

test('attemptAutoRelink never calls rotateCredentialValuesFn and returns only { status: "fallback" } on mfa_required', async () => {
  const db = createDatabase(':memory:');
  try {
    const job = seedJob(db);
    let rotateCalls = 0;
    const withPlainContextFn = async (run) => run({});
    const obtainStartUrlFn = async () => ({ status: 'mfa_required' });
    const rotateCredentialValuesFn = () => {
      rotateCalls += 1;
    };

    const result = await attemptAutoRelink(
      db,
      job,
      { username: 'someuser', password: 'somepass', masterKey: MASTER_KEY },
      { withPlainContextFn, obtainStartUrlFn, rotateCredentialValuesFn },
    );

    assert.deepEqual(result, { status: 'fallback' });
    assert.deepEqual(Object.keys(result), ['status']);
    assert.equal(rotateCalls, 0);
  } finally {
    db.close();
  }
});

test('attemptAutoRelink never calls rotateCredentialValuesFn and returns only { status: "fallback" } on a failed obtainStartUrlFn result', async () => {
  const db = createDatabase(':memory:');
  try {
    const job = seedJob(db);
    let rotateCalls = 0;
    const withPlainContextFn = async (run) => run({});
    const obtainStartUrlFn = async () => ({ status: 'failed', reason: 'link_not_found' });
    const rotateCredentialValuesFn = () => {
      rotateCalls += 1;
    };

    const result = await attemptAutoRelink(
      db,
      job,
      { username: 'someuser', password: 'somepass', masterKey: MASTER_KEY },
      { withPlainContextFn, obtainStartUrlFn, rotateCredentialValuesFn },
    );

    assert.deepEqual(result, { status: 'fallback' });
    assert.equal(rotateCalls, 0);
  } finally {
    db.close();
  }
});
