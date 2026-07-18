import { test } from 'node:test';
import assert from 'node:assert/strict';
import PQueue from 'p-queue';
import { createDatabase } from '../db/database.js';
import { upsertUser } from '../db/users.repository.js';
import { createJob } from '../db/jobs.repository.js';
import { groupActiveJobsByAccount, selectRoundRobinTick, createScheduler } from './queue.js';

const FILTROS = {
  materiaCodigo: '3.1.050',
  ofrecimiento: 'curricular',
  turno: 'Mañana',
  dias: ['LU'],
  sedesExcluidas: [],
};

test('groupActiveJobsByAccount groups an array of jobs by discordUserId', () => {
  const jobs = [
    { id: 1, discordUserId: 'a' },
    { id: 2, discordUserId: 'a' },
    { id: 3, discordUserId: 'b' },
  ];

  const grouped = groupActiveJobsByAccount(jobs);

  assert.equal(grouped.size, 2);
  assert.equal(grouped.get('a').length, 2);
  assert.equal(grouped.get('b').length, 1);
});

test('selectRoundRobinTick selects exactly one job per account per call, visiting every job of a multi-job account before repeating', () => {
  const jobsByAccount = new Map([
    [
      'a',
      [
        { id: 1, discordUserId: 'a' },
        { id: 2, discordUserId: 'a' },
        { id: 3, discordUserId: 'a' },
      ],
    ],
    ['b', [{ id: 4, discordUserId: 'b' }]],
  ]);

  let pointers = new Map();
  const visitedAIds = [];
  const visitedBIds = [];

  for (let tick = 0; tick < 3; tick += 1) {
    const { selected, nextPointers } = selectRoundRobinTick(jobsByAccount, pointers);
    pointers = nextPointers;

    const aSelections = selected.filter((job) => job.discordUserId === 'a');
    const bSelections = selected.filter((job) => job.discordUserId === 'b');

    // Exactly one job per account per call — never 0, never 2+.
    assert.equal(aSelections.length, 1);
    assert.equal(bSelections.length, 1);

    visitedAIds.push(aSelections[0].id);
    visitedBIds.push(bSelections[0].id);
  }

  // Round-robin fairness (D-03): all 3 of account 'a's jobs visited exactly
  // once each across the 3 calls, before repeating.
  assert.deepEqual([...visitedAIds].sort(), [1, 2, 3]);
  assert.equal(new Set(visitedAIds).size, 3);

  // Account 'b's single job is selected on every call.
  assert.ok(visitedBIds.every((id) => id === 4));
});

test('createScheduler.start() calls pollOnceFn on active jobs and stop() halts further calls', async () => {
  const db = createDatabase(':memory:');
  try {
    upsertUser(db, 'user-1');
    createJob(db, { discordUserId: 'user-1', filtros: FILTROS });

    let callCount = 0;
    const pollOnceFn = async () => {
      callCount += 1;
    };

    const scheduler = createScheduler({ db, intervalMs: 20, concurrency: 2, pollOnceFn });
    scheduler.start();

    // ~3 tick intervals.
    await new Promise((resolve) => setTimeout(resolve, 75));
    scheduler.stop();

    const countAtStop = callCount;
    assert.ok(countAtStop >= 1);

    // No further ticks/calls after stop().
    await new Promise((resolve) => setTimeout(resolve, 60));
    assert.equal(callCount, countAtStop);
  } finally {
    db.close();
  }
});

test('createScheduler.start({ immediate: true }) polls active jobs immediately without waiting for the first interval', async () => {
  const db = createDatabase(':memory:');
  try {
    upsertUser(db, 'user-1');
    createJob(db, { discordUserId: 'user-1', filtros: FILTROS });

    let callCount = 0;
    const pollOnceFn = async () => {
      callCount += 1;
    };

    const scheduler = createScheduler({ db, intervalMs: 200, concurrency: 2, pollOnceFn });
    scheduler.start({ immediate: true });

    // Well before the first 200ms interval would fire.
    await new Promise((resolve) => setTimeout(resolve, 40));
    scheduler.stop();

    assert.ok(callCount >= 1, 'expected an immediate poll before the first interval elapsed');
  } finally {
    db.close();
  }
});

test('createScheduler.start() without immediate does not poll before the first interval elapses', async () => {
  const db = createDatabase(':memory:');
  try {
    upsertUser(db, 'user-1');
    createJob(db, { discordUserId: 'user-1', filtros: FILTROS });

    let callCount = 0;
    const pollOnceFn = async () => {
      callCount += 1;
    };

    const scheduler = createScheduler({ db, intervalMs: 200, concurrency: 2, pollOnceFn });
    scheduler.start();

    await new Promise((resolve) => setTimeout(resolve, 40));
    scheduler.stop();

    assert.equal(callCount, 0);
  } finally {
    db.close();
  }
});

test('createScheduler calls onJobPolled with the selected job and poll outcome', async () => {
  const db = createDatabase(':memory:');
  try {
    upsertUser(db, 'user-1');
    const job = createJob(db, { discordUserId: 'user-1', filtros: FILTROS });
    const outcome = { outcome: 'no_vacancies' };
    const hookCalls = [];

    const pollOnceFn = async () => outcome;
    const onJobPolled = async (selectedJob, selectedOutcome) => {
      hookCalls.push({ selectedJob, selectedOutcome });
    };

    const scheduler = createScheduler({ db, intervalMs: 20, concurrency: 1, pollOnceFn, onJobPolled });
    scheduler.start();
    await new Promise((resolve) => setTimeout(resolve, 35));
    scheduler.stop();

    assert.ok(hookCalls.length >= 1);
    assert.equal(hookCalls[0].selectedJob.id, job.id);
    assert.equal(hookCalls[0].selectedJob.discordUserId, 'user-1');
    assert.equal(hookCalls[0].selectedOutcome, outcome);
  } finally {
    db.close();
  }
});

test('scheduler.pollJobNow enqueues a specific job immediately through the notification hook', async () => {
  const db = createDatabase(':memory:');
  try {
    upsertUser(db, 'user-1');
    const job = createJob(db, { discordUserId: 'user-1', filtros: FILTROS });
    const hookCalls = [];
    const scheduler = createScheduler({
      db,
      intervalMs: 1000000,
      concurrency: 1,
      pollOnceFn: async () => ({ outcome: 'no_vacancies' }),
      onJobPolled: async (selectedJob, outcome) => {
        hookCalls.push({ selectedJob, outcome });
      },
    });

    assert.equal(scheduler.pollJobNow(job), true);
    await new Promise((resolve) => setTimeout(resolve, 20));

    assert.equal(hookCalls.length, 1);
    assert.equal(hookCalls[0].selectedJob.id, job.id);
    assert.deepEqual(hookCalls[0].outcome, { outcome: 'no_vacancies' });
  } finally {
    db.close();
  }
});

test('scheduler.pollJobNow queues behind an in-flight poll for the same account instead of dropping the request', async () => {
  const db = createDatabase(':memory:');
  try {
    upsertUser(db, 'user-1');
    const jobA = createJob(db, { discordUserId: 'user-1', filtros: FILTROS, label: 'A' });
    const jobB = createJob(db, { discordUserId: 'user-1', filtros: { ...FILTROS, materiaCodigo: '3.4.219' }, label: 'B' });

    let releaseJobA;
    const jobAGate = new Promise((resolve) => {
      releaseJobA = resolve;
    });
    let signalJobAStarted;
    const jobAStarted = new Promise((resolve) => {
      signalJobAStarted = resolve;
    });

    const polled = [];
    const scheduler = createScheduler({
      db,
      intervalMs: 1000000,
      concurrency: 2,
      pollOnceFn: async (_db, jobId) => {
        if (jobId === jobA.id) {
          signalJobAStarted();
          await jobAGate;
        }
        polled.push(jobId);
        return { outcome: 'no_vacancies' };
      },
      onJobPolled: async () => {},
    });

    assert.equal(scheduler.pollJobNow(jobA), true);
    await jobAStarted;

    // jobA's poll is now in-flight (blocked on its own gate) -- this
    // immediate request for jobB (same account) must be queued behind it,
    // not silently dropped.
    assert.equal(scheduler.pollJobNow(jobB), false);

    releaseJobA();
    await new Promise((resolve) => setTimeout(resolve, 30));

    assert.deepEqual(polled, [jobA.id, jobB.id]);
  } finally {
    db.close();
  }
});

test('scheduler.pollJobNow drains four same-account immediate jobs once in order', async () => {
  const db = createDatabase(':memory:');
  try {
    upsertUser(db, 'user-1');
    const jobs = ['3.1.050', '3.1.051', '3.1.052', '3.1.053'].map((materiaCodigo, index) =>
      createJob(db, { discordUserId: 'user-1', filtros: { ...FILTROS, materiaCodigo }, label: String(index) }));

    let releaseFirst;
    const firstGate = new Promise((resolve) => {
      releaseFirst = resolve;
    });
    let signalFirstStarted;
    const firstStarted = new Promise((resolve) => {
      signalFirstStarted = resolve;
    });
    let signalAllPolled;
    const allPolled = new Promise((resolve) => {
      signalAllPolled = resolve;
    });
    const polled = [];
    const scheduler = createScheduler({
      db,
      intervalMs: 1000000,
      concurrency: 2,
      pollOnceFn: async (_db, jobId) => {
        if (jobId === jobs[0].id) {
          signalFirstStarted();
          await firstGate;
        }
        polled.push(jobId);
        if (polled.length === jobs.length) signalAllPolled();
        return { outcome: 'no_vacancies' };
      },
    });

    assert.equal(scheduler.pollJobNow(jobs[0]), true);
    await firstStarted;
    for (const job of jobs.slice(1)) {
      assert.equal(scheduler.pollJobNow(job), false);
    }
    assert.equal(scheduler.pollJobNow(jobs[1]), false, 'duplicate job id is deduplicated');
    releaseFirst();
    await Promise.race([
      allPolled,
      new Promise((_, reject) => setTimeout(() => reject(new Error('immediate queue did not drain')), 500)),
    ]);

    assert.deepEqual(polled, jobs.map((job) => job.id));
  } finally {
    db.close();
  }
});

test('createScheduler does not call onJobPolled when pollOnceFn rejects', async () => {
  const db = createDatabase(':memory:');
  try {
    upsertUser(db, 'user-1');
    createJob(db, { discordUserId: 'user-1', filtros: FILTROS });
    let hookCalls = 0;

    const scheduler = createScheduler({
      db,
      intervalMs: 20,
      concurrency: 1,
      pollOnceFn: async () => {
        throw new Error('poll failed');
      },
      onJobPolled: async () => {
        hookCalls += 1;
      },
    });
    scheduler.start();
    await new Promise((resolve) => setTimeout(resolve, 45));
    scheduler.stop();

    assert.equal(hookCalls, 0);
  } finally {
    db.close();
  }
});

test('createScheduler logs onJobPolled failures and continues later ticks', async () => {
  const db = createDatabase(':memory:');
  try {
    upsertUser(db, 'user-1');
    createJob(db, { discordUserId: 'user-1', filtros: FILTROS });
    let pollCalls = 0;
    let hookCalls = 0;

    const scheduler = createScheduler({
      db,
      intervalMs: 20,
      concurrency: 1,
      pollOnceFn: async () => {
        pollCalls += 1;
        return { outcome: 'no_vacancies' };
      },
      onJobPolled: async () => {
        hookCalls += 1;
        if (hookCalls === 1) {
          throw new Error('send failed');
        }
      },
    });
    scheduler.start();
    await new Promise((resolve) => setTimeout(resolve, 75));
    scheduler.stop();

    assert.ok(pollCalls >= 2);
    assert.ok(hookCalls >= 2);
  } finally {
    db.close();
  }
});

test('a PQueue with concurrency N never lets .pending exceed N', async () => {
  const queue = new PQueue({ concurrency: 2 });
  let maxPending = 0;

  const sampleInterval = setInterval(() => {
    maxPending = Math.max(maxPending, queue.pending);
  }, 5);

  const job = () => new Promise((resolve) => setTimeout(resolve, 30));
  const promises = Array.from({ length: 5 }, () => queue.add(job));
  maxPending = Math.max(maxPending, queue.pending);

  await Promise.all(promises);
  clearInterval(sampleInterval);

  assert.ok(maxPending <= 2);
});

test('createScheduler does not manage browser lifecycle when a tick finds no jobs to poll', async () => {
  const db = createDatabase(':memory:');
  try {
    let closeCalls = 0;
    const closeBrowserFn = async () => {
      closeCalls += 1;
    };

    const scheduler = createScheduler({ db, intervalMs: 1000, concurrency: 1, closeBrowserFn });
    scheduler.start({ immediate: true });
    scheduler.stop();

    assert.equal(closeCalls, 0);
  } finally {
    db.close();
  }
});
