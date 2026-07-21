import { test } from 'node:test';
import assert from 'node:assert/strict';
import { randomBytes } from 'node:crypto';
import { readFile } from 'node:fs/promises';
import { createDatabase } from '../db/database.js';
import { upsertUser, getUser } from '../db/users.repository.js';
import { upsertCredentials } from '../db/credentials.repository.js';
import { createJob, getJob } from '../db/jobs.repository.js';
import { getMateriaNombre } from '../db/materias.repository.js';
import { listHistoryForJob } from '../db/poll-history.repository.js';
import { encryptCredentials } from '../crypto/credentials-crypto.js';
import { pollOnce } from './poller.js';

const MASTER_KEY = randomBytes(32).toString('hex');

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

function pollDeps(runHttpSearchFn, overrides = {}) {
  return {
    masterKey: MASTER_KEY,
    withHttpSessionFn: async (credentials, run) => run({ request: async () => { throw new Error('unexpected request'); } }),
    runHttpSearchFn,
    ...overrides,
  };
}

test('pollOnce requires an explicit masterKey and never falls back to process configuration', async () => {
  const db = createDatabase(':memory:');
  try {
    const job = seedJob(db);
    await assert.rejects(
      pollOnce(db, job.id, {
        withHttpSessionFn: async (credentials, run) => run({}),
        runHttpSearchFn: async () => ({ status: 'verified', html: '<table id="results"></table>' }),
      }),
      /masterKey/,
    );
  } finally {
    db.close();
  }
});

test('pollOnce persists a no_vacancies outcome for a verified search with zero parsed vacancies', async () => {
  const db = createDatabase(':memory:');
  try {
    const job = seedJob(db);
    const before = Date.now();

    const runHttpSearchFn = async () => ({
      status: 'verified',
      html: '<table id="results"></table>',
      materiaNombre: 'Fisica II',
    });
    await pollOnce(db, job.id, pollDeps(runHttpSearchFn));

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
    const noVacancies = async () => ({ status: 'verified', html: '<table id="results"></table>' });

    await pollOnce(db, job.id, pollDeps(noVacancies));
    await pollOnce(db, job.id, pollDeps(noVacancies));

    assert.equal(listHistoryForJob(db, job.id).length, 1);

    const invalidCredentials = async () => ({ status: 'invalid_credentials' });
    await pollOnce(db, job.id, pollDeps(invalidCredentials));

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

    const runHttpSearchFn = async () => ({ status: 'verified', html: '<table id="results"></table>' });
    await pollOnce(db, job.id, pollDeps(runHttpSearchFn));

    assert.equal(getMateriaNombre(db, FILTROS.materiaCodigo), null);
  } finally {
    db.close();
  }
});

test('pollOnce persists an invalid_credentials outcome', async () => {
  const db = createDatabase(':memory:');
  try {
    const job = seedJob(db);

    const runHttpSearchFn = async () => ({ status: 'invalid_credentials' });
    await pollOnce(db, job.id, pollDeps(runHttpSearchFn));

    const updated = getJob(db, job.id);
    assert.equal(updated.lastOutcome, JSON.stringify({ outcome: 'invalid_credentials' }));
  } finally {
    db.close();
  }
});

test('pollOnce preserves search_failed mismatch instead of classifying an unverified response as no_vacancies', async () => {
  const db = createDatabase(':memory:');
  try {
    const job = seedJob(db);
    const runHttpSearchFn = async () => ({ status: 'search_failed', reason: 'postback_mismatch' });

    const outcome = await pollOnce(db, job.id, pollDeps(runHttpSearchFn));

    assert.deepEqual(outcome, { outcome: 'search_failed', reason: 'postback_mismatch' });
    assert.equal(getJob(db, job.id).lastOutcome, JSON.stringify(outcome));
    assert.equal(getUser(db, job.discordUserId).pauseReason, null);
  } finally {
    db.close();
  }
});

test('pollOnce fails closed when result rows are invalid or the empty-results structure is missing', async () => {
  for (const html of [
    '<table class="grillaInscripcion"><tr class="row_central"><td>changed markup</td></tr></table>',
    '<html><body><div>unexpected portal response</div></body></html>',
  ]) {
    const db = createDatabase(':memory:');
    try {
      const job = seedJob(db);
      const runHttpSearchFn = async () => ({ status: 'verified', html });

      const outcome = await pollOnce(db, job.id, pollDeps(runHttpSearchFn));

      assert.deepEqual(outcome, { outcome: 'search_failed', reason: 'result_parse_failed' });
      assert.equal(getJob(db, job.id).lastOutcome, JSON.stringify(outcome));
    } finally {
      db.close();
    }
  }
});

test('pollOnce preserves the rate_limited account backoff state', async () => {
  const db = createDatabase(':memory:');
  try {
    const job = seedJob(db);
    const runHttpSearchFn = async () => ({ status: 'rate_limited' });

    const outcome = await pollOnce(db, job.id, pollDeps(runHttpSearchFn));

    assert.deepEqual(outcome, { outcome: 'rate_limited' });
    const user = getUser(db, job.discordUserId);
    assert.equal(user.pauseReason, 'rate_limited');
    assert.equal(user.backoffAttempt, 1);
    assert.ok(user.pauseUntil > Date.now());
  } finally {
    db.close();
  }
});

test('pollOnce never leaves plaintext credentials in any DB table after resolving', async () => {
  const db = createDatabase(':memory:');
  try {
    const job = seedJob(db);

    const runHttpSearchFn = async () => ({ status: 'verified', html: '<table id="results"></table>' });
    await pollOnce(db, job.id, pollDeps(runHttpSearchFn));

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

test('pollOnce decrypts credentials transiently and passes them to withHttpSessionFn/runHttpSearchFn', async () => {
  const db = createDatabase(':memory:');
  try {
    const job = seedJob(db);

    let capturedCreds;
    let capturedSearchArgs;

    const withHttpSessionFn = async (creds, run) => {
      capturedCreds = creds;
      return run({});
    };
    const runHttpSearchFn = async (session, filtros, options) => {
      capturedSearchArgs = { filtros, options };
      return { status: 'verified', html: '<table id="results"></table>' };
    };

    await pollOnce(db, job.id, { masterKey: MASTER_KEY, withHttpSessionFn, runHttpSearchFn });

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

    const runHttpSearchFn = async () => ({ status: 'invalid_credentials' });
    await pollOnce(db, job.id, pollDeps(runHttpSearchFn));

    const after = getUser(db, job.discordUserId);
    assert.equal(after.pauseReason, 'needs_credentials');
  } finally {
    db.close();
  }
});

// --- Browserless relink policy ------------------------------------------

test('pollOnce has no relink loader path for a verified/no_vacancies outcome', async () => {
  const db = createDatabase(':memory:');
  try {
    const job = seedJob(db);
    const runHttpSearchFn = async () => ({ status: 'verified', html: '<table id="results"></table>' });
    await pollOnce(db, job.id, pollDeps(runHttpSearchFn));

    assert.equal(getUser(db, job.discordUserId).pauseReason, null);
  } finally {
    db.close();
  }
});

test('pollOnce has no relink loader path for invalid_credentials/rate_limited/search_failed outcomes', async () => {
  const db = createDatabase(':memory:');
  try {
    const job = seedJob(db);
    for (const status of ['invalid_credentials', 'rate_limited', 'search_failed']) {
      const runHttpSearchFn = async () => (status === 'search_failed' ? { status, reason: 'postback_timeout' } : { status });
      await pollOnce(db, job.id, pollDeps(runHttpSearchFn));
      assert.ok(getUser(db, job.discordUserId), `user row must exist for outcome "${status}"`);
    }
  } finally {
    db.close();
  }
});

test('pollOnce maps stale_start_url directly to needs_new_start_url without browser relink', async () => {
  const db = createDatabase(':memory:');
  try {
    const job = seedJob(db);
    const runHttpSearchFn = async () => ({ status: 'stale_start_url' });
    const outcome = await pollOnce(db, job.id, pollDeps(runHttpSearchFn));

    assert.deepEqual(outcome, { outcome: 'stale_start_url' });
    assert.equal(getUser(db, job.discordUserId).pauseReason, 'needs_new_start_url');
    assert.equal(getJob(db, job.id).lastOutcome, JSON.stringify({ outcome: 'stale_start_url' }));
  } finally {
    db.close();
  }
});

test('poller composition has no browser, SSO, or relink module boundary', async () => {
  const source = await readFile(new URL('./poller.js', import.meta.url), 'utf8');

  assert.doesNotMatch(source, /^import .*automation\/browser\.js/m);
  assert.doesNotMatch(source, /^import .*automation\/sso-link\.js/m);
  assert.doesNotMatch(source, /^import .*\.\/relink\.js/m);
  assert.doesNotMatch(source, /import\(['"].*relink.*['"]\)/);
  assert.doesNotMatch(source, /attemptAutoRelink|withPlainContext|obtainStartUrl/);
});
