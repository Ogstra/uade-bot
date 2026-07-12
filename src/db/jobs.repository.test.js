import assert from 'node:assert/strict';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { test } from 'node:test';
import Database from 'better-sqlite3';
import { createDatabase } from './database.js';
import {
  createJob,
  deleteJob,
  getJob,
  listActiveJobs,
  listJobsByUser,
  updateJobPollResult,
  updateJobStatus,
} from './jobs.repository.js';
import { upsertUser } from './users.repository.js';

const FILTROS = {
  materiaCodigo: '3.1.050',
  ofrecimiento: 'curricular',
  turno: 'Mañana',
  dias: ['LU', 'MI'],
  sedesExcluidas: ['Monserrat'],
};

test('createJob persists channelId and label, defaulting label to materiaCodigo', () => {
  const db = createDatabase(':memory:');
  try {
    upsertUser(db, 'user-1');

    const labeled = createJob(db, {
      discordUserId: 'user-1',
      filtros: FILTROS,
      channelId: 'channel-1',
      label: 'Fisica II',
    });
    const defaultLabeled = createJob(db, {
      discordUserId: 'user-1',
      filtros: { ...FILTROS, materiaCodigo: '3.4.219' },
      channelId: null,
    });

    assert.equal(labeled.channelId, 'channel-1');
    assert.equal(labeled.label, 'Fisica II');
    assert.equal(defaultLabeled.channelId, null);
    assert.equal(defaultLabeled.label, '3.4.219');
  } finally {
    db.close();
  }
});

test('listJobsByUser returns only active and paused jobs for that user with poll fields', () => {
  const db = createDatabase(':memory:');
  try {
    upsertUser(db, 'user-1');
    upsertUser(db, 'user-2');
    const job1 = createJob(db, {
      discordUserId: 'user-1',
      filtros: FILTROS,
      channelId: 'channel-1',
      label: 'Fisica II',
    });
    const job2 = createJob(db, {
      discordUserId: 'user-1',
      filtros: { ...FILTROS, materiaCodigo: '3.4.219' },
      channelId: 'channel-2',
      label: 'Algebra',
    });
    createJob(db, {
      discordUserId: 'user-2',
      filtros: FILTROS,
      channelId: 'channel-3',
      label: 'Otra',
    });

    updateJobStatus(db, job2.id, 'paused_by_user');
    updateJobPollResult(db, job1.id, { lastPolledAt: 12345, lastOutcome: 'no_vacancies' });

    const jobs = listJobsByUser(db, 'user-1');

    assert.deepEqual(
      jobs.map((job) => job.id),
      [job1.id, job2.id],
    );
    assert.equal(jobs[0].lastPolledAt, 12345);
    assert.equal(jobs[0].lastOutcome, 'no_vacancies');
    assert.equal(jobs[1].status, 'paused_by_user');
  } finally {
    db.close();
  }
});

test('updateJobStatus preserves filtros and listActiveJobs still excludes paused jobs', () => {
  const db = createDatabase(':memory:');
  try {
    upsertUser(db, 'user-1');
    const job = createJob(db, {
      discordUserId: 'user-1',
      filtros: FILTROS,
      channelId: 'channel-1',
      label: 'Fisica II',
    });

    const updated = updateJobStatus(db, job.id, 'paused_by_user');

    assert.equal(updated.status, 'paused_by_user');
    assert.deepEqual(updated.filtros, FILTROS);
    assert.deepEqual(listActiveJobs(db), []);

    const resumed = updateJobStatus(db, job.id, 'active');

    assert.equal(resumed.status, 'active');
    assert.deepEqual(listActiveJobs(db).map((activeJob) => activeJob.id), [job.id]);
  } finally {
    db.close();
  }
});

test('deleteJob removes a job row and createJob still enforces users foreign key', () => {
  const db = createDatabase(':memory:');
  try {
    upsertUser(db, 'user-1');
    const job = createJob(db, {
      discordUserId: 'user-1',
      filtros: FILTROS,
      channelId: 'channel-1',
      label: 'Fisica II',
    });

    assert.equal(deleteJob(db, job.id), true);
    assert.equal(getJob(db, job.id), null);
    assert.equal(deleteJob(db, job.id), false);
    assert.throws(() =>
      createJob(db, {
        discordUserId: 'missing-user',
        filtros: FILTROS,
        channelId: null,
      }),
    );
  } finally {
    db.close();
  }
});

test('createDatabase migrates existing jobs tables with channel_id and label columns', () => {
  const dir = mkdtempSync(join(tmpdir(), 'uade-bot-jobs-'));
  const dbPath = join(dir, 'old.db');
  const oldDb = new Database(dbPath);
  try {
    oldDb.pragma('foreign_keys = ON');
    oldDb.exec(`
      CREATE TABLE users (
        discord_user_id TEXT PRIMARY KEY,
        pause_reason TEXT,
        pause_until INTEGER,
        backoff_attempt INTEGER NOT NULL DEFAULT 0,
        created_at INTEGER NOT NULL,
        updated_at INTEGER NOT NULL
      );
      CREATE TABLE jobs (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        discord_user_id TEXT NOT NULL REFERENCES users(discord_user_id),
        filtros_json TEXT NOT NULL,
        status TEXT NOT NULL DEFAULT 'active',
        last_polled_at INTEGER,
        last_outcome TEXT,
        created_at INTEGER NOT NULL
      );
    `);
  } finally {
    oldDb.close();
  }

  const db = createDatabase(dbPath);
  try {
    const columns = db.prepare("PRAGMA table_info('jobs')").all().map((column) => column.name);
    assert.equal(columns.includes('channel_id'), true);
    assert.equal(columns.includes('label'), true);

    upsertUser(db, 'user-1');
    const job = createJob(db, { discordUserId: 'user-1', filtros: FILTROS, channelId: 'channel-1' });

    assert.equal(job.channelId, 'channel-1');
    assert.equal(job.label, FILTROS.materiaCodigo);
  } finally {
    db.close();
    rmSync(dir, { recursive: true, force: true });
  }
});
