import { test } from 'node:test';
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { randomBytes } from 'node:crypto';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createDatabase } from '../db/database.js';
import { upsertUser } from '../db/users.repository.js';
import { createJob } from '../db/jobs.repository.js';
import { createScheduler } from './queue.js';
import { reconstructActiveJobs } from './bootstrap.js';

const FILTROS = {
  materiaCodigo: '3.1.050',
  ofrecimiento: 'curricular',
  turno: 'Mañana',
  dias: ['LU'],
  sedesExcluidas: [],
};

function seedJobs(db) {
  upsertUser(db, 'user-1');
  upsertUser(db, 'user-2');

  const job1 = createJob(db, { discordUserId: 'user-1', filtros: FILTROS });
  const job2 = createJob(db, { discordUserId: 'user-1', filtros: FILTROS });
  const job3 = createJob(db, { discordUserId: 'user-2', filtros: FILTROS });
  const pausedJob = createJob(db, { discordUserId: 'user-2', filtros: FILTROS });
  db.prepare("UPDATE jobs SET status = 'paused_by_user' WHERE id = ?").run(pausedJob.id);

  return { job1, job2, job3, pausedJob };
}

test('reconstructActiveJobs registers exactly the 3 previously-active jobs, excluding the paused_by_user job', async () => {
  const db = createDatabase(':memory:');
  try {
    const { job1, job2, job3, pausedJob } = seedJobs(db);
    const scheduler = createScheduler({ db, intervalMs: 1000000, concurrency: 1, pollOnceFn: async () => {} });

    await reconstructActiveJobs(db, scheduler);

    assert.equal(scheduler.isRegistered(job1.id), true);
    assert.equal(scheduler.isRegistered(job2.id), true);
    assert.equal(scheduler.isRegistered(job3.id), true);
    assert.equal(scheduler.isRegistered(pausedJob.id), false);
  } finally {
    db.close();
  }
});

test('reconstructActiveJobs called twice against the same db/scheduler never re-registers an already-registered job (SCHED-04)', async () => {
  const db = createDatabase(':memory:');
  try {
    const { job1, job2, job3 } = seedJobs(db);
    const scheduler = createScheduler({ db, intervalMs: 1000000, concurrency: 1, pollOnceFn: async () => {} });

    await reconstructActiveJobs(db, scheduler);
    assert.equal(scheduler.isRegistered(job1.id), true);
    assert.equal(scheduler.isRegistered(job2.id), true);
    assert.equal(scheduler.isRegistered(job3.id), true);

    // Spy on registerJob to prove the second "restart" makes zero new
    // registration calls — every job id it sees was already registered.
    let registerCallsOnSecondRun = 0;
    const originalRegisterJob = scheduler.registerJob.bind(scheduler);
    scheduler.registerJob = (job) => {
      registerCallsOnSecondRun += 1;
      return originalRegisterJob(job);
    };

    await reconstructActiveJobs(db, scheduler);

    assert.equal(registerCallsOnSecondRun, 0);
    assert.equal(scheduler.isRegistered(job1.id), true);
    assert.equal(scheduler.isRegistered(job2.id), true);
    assert.equal(scheduler.isRegistered(job3.id), true);
  } finally {
    db.close();
  }
});

test('scheduler.isRegistered returns true only for ids that went through registerJob, false for an unknown id', () => {
  const db = createDatabase(':memory:');
  try {
    const scheduler = createScheduler({ db, intervalMs: 1000000, concurrency: 1, pollOnceFn: async () => {} });

    assert.equal(scheduler.isRegistered(999), false);
    scheduler.registerJob({ id: 999 });
    assert.equal(scheduler.isRegistered(999), true);
    assert.equal(scheduler.isRegistered(123), false);
  } finally {
    db.close();
  }
});

test('spawning node src/scheduler.js stays alive and logs scheduler_started within 2 seconds', async () => {
  const tmpDir = mkdtempSync(join(tmpdir(), 'uade-bot-scheduler-test-'));
  const dbPath = join(tmpDir, 'test.db');

  const child = spawn(
    process.execPath,
    ['src/scheduler.js'],
    {
      cwd: process.cwd(),
      env: {
        ...process.env,
        DATABASE_PATH: dbPath,
        UADE_USERNAME: 'dummy-user',
        UADE_PASSWORD: 'dummy-pass',
        CREDENTIALS_MASTER_KEY: randomBytes(32).toString('hex'),
        POLL_INTERVAL_MS: '1000000',
      },
    },
  );

  let stdout = '';
  child.stdout.on('data', (chunk) => {
    stdout += chunk.toString();
  });
  let stderr = '';
  child.stderr.on('data', (chunk) => {
    stderr += chunk.toString();
  });

  let exited = false;
  child.on('exit', () => {
    exited = true;
  });

  try {
    const sawStartedLog = await new Promise((resolve) => {
      const start = Date.now();
      const poll = setInterval(() => {
        if (stdout.includes('scheduler_started')) {
          clearInterval(poll);
          resolve(true);
        } else if (exited || Date.now() - start > 2000) {
          clearInterval(poll);
          resolve(false);
        }
      }, 50);
    });

    assert.equal(sawStartedLog, true, `expected stdout to contain scheduler_started; stdout=${stdout} stderr=${stderr}`);
    assert.equal(exited, false, `scheduler.js process exited unexpectedly; stdout=${stdout} stderr=${stderr}`);
  } finally {
    child.kill();
    rmSync(tmpDir, { recursive: true, force: true });
  }
});
