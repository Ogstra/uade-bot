import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createDatabase } from './database.js';
import { createJob, getJob, listActiveJobs } from './jobs.repository.js';
import { upsertUser, getUser } from './users.repository.js';

test('createDatabase is idempotent against the same on-disk path', () => {
  const dir = mkdtempSync(join(tmpdir(), 'uade-bot-db-test-'));
  const dbPath = join(dir, 'test.db');

  try {
    const db1 = createDatabase(dbPath);
    db1.close();

    assert.doesNotThrow(() => {
      const db2 = createDatabase(dbPath);
      db2.close();
    });
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('createJob + getJob round-trips filtros through JSON storage', () => {
  const db = createDatabase(':memory:');
  try {
    upsertUser(db, 'user-1');
    const filtros = {
      materiaCodigo: '3.1.050',
      ofrecimiento: 'curricular',
      turno: 'Mañana',
      dias: ['LU', 'MI'],
      sedesExcluidas: ['Recoleta'],
    };

    const created = createJob(db, { discordUserId: 'user-1', filtros });
    const fetched = getJob(db, created.id);

    assert.deepEqual(fetched.filtros, filtros);
  } finally {
    db.close();
  }
});

test('listActiveJobs excludes a job explicitly created with paused_by_user status', () => {
  const db = createDatabase(':memory:');
  try {
    upsertUser(db, 'user-1');
    const filtros = {
      materiaCodigo: '3.1.050',
      ofrecimiento: 'curricular',
      turno: 'Mañana',
      dias: ['LU'],
    };

    createJob(db, { discordUserId: 'user-1', filtros });

    const now = Date.now();
    db.prepare(
      `INSERT INTO jobs (discord_user_id, filtros_json, status, created_at)
       VALUES (?, ?, 'paused_by_user', ?)`,
    ).run('user-1', JSON.stringify(filtros), now);

    const active = listActiveJobs(db);

    assert.equal(active.length, 1);
    assert.equal(active[0].status, 'active');
  } finally {
    db.close();
  }
});

test('getUser returns null for an unknown id and a validated row after upsertUser', () => {
  const db = createDatabase(':memory:');
  try {
    assert.equal(getUser(db, 'ghost-user'), null);

    const user = upsertUser(db, 'user-2');

    assert.equal(user.discordUserId, 'user-2');
    assert.equal(user.pauseReason, null);
    assert.equal(user.backoffAttempt, 0);
  } finally {
    db.close();
  }
});
