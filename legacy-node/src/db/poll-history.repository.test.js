import assert from 'node:assert/strict';
import { test } from 'node:test';
import { PollOutcomeHistoryRecordSchema } from '../schemas.js';
import { createDatabase } from './database.js';
import { createJob, deleteJob, getJob } from './jobs.repository.js';
import { listHistoryForJob, persistPollResult } from './poll-history.repository.js';
import { upsertUser } from './users.repository.js';

const FILTROS = {
  materiaCodigo: '3.1.050',
  ofrecimiento: 'curricular',
  turno: 'Mañana',
  dias: ['LU'],
  sedesExcluidas: [],
};

function seedJob(db) {
  upsertUser(db, 'history-user');
  return createJob(db, { discordUserId: 'history-user', filtros: FILTROS });
}

function vacancy(cupos) {
  return {
    turno: 'Mañana',
    sede: 'Monserrat',
    horario: '08:00',
    dias: ['LU'],
    cupos,
  };
}

test('first outcome is recorded, repeated discriminator is ignored, and changed outcome is newest-first', () => {
  const db = createDatabase(':memory:');
  try {
    const job = seedJob(db);
    persistPollResult(db, {
      jobId: job.id,
      recordedAt: 100,
      outcome: { outcome: 'found', vacancies: [vacancy(2), vacancy(3)] },
    });
    persistPollResult(db, {
      jobId: job.id,
      recordedAt: 200,
      outcome: { outcome: 'found', vacancies: [vacancy(99)] },
    });
    persistPollResult(db, {
      jobId: job.id,
      recordedAt: 300,
      outcome: { outcome: 'no_vacancies' },
    });

    assert.deepEqual(listHistoryForJob(db, job.id), [
      {
        id: 2,
        jobId: job.id,
        recordedAt: 300,
        outcomeCode: 'no_vacancies',
        vacancyCount: null,
        totalCupos: null,
      },
      {
        id: 1,
        jobId: job.id,
        recordedAt: 100,
        outcomeCode: 'found',
        vacancyCount: 2,
        totalCupos: 5,
      },
    ]);
    assert.equal(getJob(db, job.id).lastPolledAt, 300);
    assert.equal(getJob(db, job.id).lastOutcome, JSON.stringify({ outcome: 'no_vacancies' }));
  } finally {
    db.close();
  }
});

test('history retains exactly ten changes and supports a bound newest-first limit', () => {
  const db = createDatabase(':memory:');
  try {
    const job = seedJob(db);
    for (let index = 0; index < 12; index += 1) {
      persistPollResult(db, {
        jobId: job.id,
        recordedAt: index + 1,
        outcome: { outcome: index % 2 === 0 ? 'no_vacancies' : 'invalid_credentials' },
      });
    }

    const history = listHistoryForJob(db, job.id);
    assert.equal(history.length, 10);
    assert.deepEqual(history.map((record) => record.recordedAt), [12, 11, 10, 9, 8, 7, 6, 5, 4, 3]);
    assert.deepEqual(listHistoryForJob(db, job.id, { limit: 3 }).map((record) => record.recordedAt), [12, 11, 10]);
    assert.throws(() => listHistoryForJob(db, job.id, { limit: '3; DROP TABLE jobs' }));
  } finally {
    db.close();
  }
});

test('cascade delete removes history rows and a failed insert rolls back current state and history', () => {
  const db = createDatabase(':memory:');
  try {
    const job = seedJob(db);
    persistPollResult(db, {
      jobId: job.id,
      recordedAt: 100,
      outcome: { outcome: 'no_vacancies' },
    });

    db.exec(`
      CREATE TRIGGER fail_history_insert
      BEFORE INSERT ON poll_outcome_history
      WHEN NEW.outcome_code = 'invalid_credentials'
      BEGIN
        SELECT RAISE(ABORT, 'forced history failure');
      END;
    `);

    assert.throws(
      () => persistPollResult(db, {
        jobId: job.id,
        recordedAt: 200,
        outcome: { outcome: 'invalid_credentials' },
      }),
      /forced history failure/,
    );
    assert.equal(getJob(db, job.id).lastPolledAt, 100);
    assert.equal(getJob(db, job.id).lastOutcome, JSON.stringify({ outcome: 'no_vacancies' }));
    assert.equal(listHistoryForJob(db, job.id).length, 1);

    db.exec('DROP TRIGGER fail_history_insert');
    assert.equal(deleteJob(db, job.id), true);
    assert.deepEqual(listHistoryForJob(db, job.id), []);
  } finally {
    db.close();
  }
});

test('history validation and storage allowlist exclude raw outcomes, reasons, credentials, and URLs', () => {
  const db = createDatabase(':memory:');
  try {
    const job = seedJob(db);
    const sentinel = 'password=secret&param=credential-equivalent';
    persistPollResult(db, {
      jobId: job.id,
      recordedAt: 100,
      outcome: { outcome: 'search_failed', reason: sentinel, uadeStartUrl: `https://example.test/?${sentinel}` },
    });

    const rawRows = db.prepare('SELECT * FROM poll_outcome_history').all();
    assert.equal(JSON.stringify(rawRows).includes(sentinel), false);
    assert.deepEqual(Object.keys(rawRows[0]), [
      'id',
      'job_id',
      'recorded_at',
      'outcome_code',
      'vacancy_count',
      'total_cupos',
    ]);
    assert.doesNotThrow(() => PollOutcomeHistoryRecordSchema.parse(listHistoryForJob(db, job.id)[0]));
    assert.throws(() => persistPollResult(db, {
      jobId: job.id,
      recordedAt: 200,
      outcome: { outcome: 'unknown_outcome' },
    }));
  } finally {
    db.close();
  }
});
