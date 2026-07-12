import assert from 'node:assert/strict';
import { test } from 'node:test';
import { createDatabase } from '../db/database.js';
import { createJob, getJob, updateJobNotifiedState } from '../db/jobs.repository.js';
import { getUser, updateAccountPauseState, upsertUser } from '../db/users.repository.js';
import {
  createNotificationDispatcher,
  notificationDecision,
} from './notifications.js';

const FILTROS = {
  materiaCodigo: '3.1.050',
  ofrecimiento: 'curricular',
  turno: 'Mañana',
  dias: ['LU'],
  sedesExcluidas: [],
};

const VACANCY = {
  turno: 'Mañana',
  sede: 'Monserrat',
  horario: '08:00 11:30',
  dias: ['LU', 'MI'],
  cupos: 2,
};

function createFakeClient() {
  const sends = [];
  return {
    sends,
    users: {
      async fetch(id) {
        return {
          async send(content) {
            sends.push({ destination: 'dm', id, content, at: Date.now() });
          },
        };
      },
    },
    channels: {
      async fetch(id) {
        return {
          async send(content) {
            sends.push({ destination: 'channel', id, content, at: Date.now() });
          },
        };
      },
    },
  };
}

test('notificationDecision suppresses unchanged vacancies, renotifies cupo increases, and clears on no_vacancies', () => {
  const first = notificationDecision(
    { lastNotifiedState: null, lastNotifiedCupos: null },
    { outcome: 'found', vacancies: [VACANCY] },
  );
  assert.equal(first.action, 'notify');
  assert.equal(first.nextCupos, 2);

  const unchanged = notificationDecision(
    { lastNotifiedState: first.nextState, lastNotifiedCupos: 2 },
    { outcome: 'found', vacancies: [{ ...VACANCY, cupos: 2 }] },
  );
  assert.equal(unchanged.action, 'suppress');

  const increased = notificationDecision(
    { lastNotifiedState: first.nextState, lastNotifiedCupos: 2 },
    { outcome: 'found', vacancies: [{ ...VACANCY, cupos: 4 }] },
  );
  assert.equal(increased.action, 'notify');
  assert.equal(increased.nextCupos, 4);

  const closed = notificationDecision(
    { lastNotifiedState: first.nextState, lastNotifiedCupos: 4 },
    { outcome: 'no_vacancies' },
  );
  assert.equal(closed.action, 'clear');
});

test('dispatcher sends Spanish DM and channel mention, then persists notification state', async () => {
  const db = createDatabase(':memory:');
  try {
    upsertUser(db, 'user-1');
    const job = createJob(db, {
      discordUserId: 'user-1',
      filtros: FILTROS,
      channelId: 'channel-1',
      label: 'Fisica II',
    });
    const client = createFakeClient();
    const dispatcher = createNotificationDispatcher({ client, db, delayMs: 0 });

    await dispatcher.onJobPolled(job, { outcome: 'found', vacancies: [VACANCY] });

    assert.equal(client.sends.length, 2);
    assert.equal(client.sends[0].destination, 'dm');
    assert.equal(client.sends[1].destination, 'channel');
    assert.match(client.sends[0].content, /Fisica II/);
    assert.match(client.sends[0].content, /Monserrat/);
    assert.match(client.sends[0].content, /08:00 11:30/);
    assert.match(client.sends[0].content, /LU, MI/);
    assert.match(client.sends[0].content, /2 cupos/);
    assert.match(client.sends[1].content, /<@user-1>/);

    const updated = getJob(db, job.id);
    assert.equal(updated.lastNotifiedCupos, 2);
    assert.notEqual(updated.lastNotifiedState, null);
  } finally {
    db.close();
  }
});

test('dispatcher clears notification state on closure so a later reopen notifies again', async () => {
  const db = createDatabase(':memory:');
  try {
    upsertUser(db, 'user-1');
    const job = createJob(db, { discordUserId: 'user-1', filtros: FILTROS, channelId: null });
    const client = createFakeClient();
    const dispatcher = createNotificationDispatcher({ client, db, delayMs: 0 });

    await dispatcher.onJobPolled(job, { outcome: 'found', vacancies: [VACANCY] });
    await dispatcher.onJobPolled(getJob(db, job.id), { outcome: 'no_vacancies' });
    await dispatcher.onJobPolled(getJob(db, job.id), { outcome: 'found', vacancies: [VACANCY] });

    assert.equal(client.sends.length, 2);
    assert.notEqual(getJob(db, job.id).lastNotifiedState, null);
  } finally {
    db.close();
  }
});

test('dispatcher sends account-pause DM only once per pause transition and clears after recovery', async () => {
  const db = createDatabase(':memory:');
  try {
    upsertUser(db, 'user-1');
    const job = createJob(db, { discordUserId: 'user-1', filtros: FILTROS, channelId: null });
    const client = createFakeClient();
    const dispatcher = createNotificationDispatcher({ client, db, delayMs: 0 });

    updateAccountPauseState(db, 'user-1', {
      pauseReason: 'needs_credentials',
      pauseUntil: null,
      backoffAttempt: 0,
    });

    await dispatcher.onJobPolled(job, { outcome: 'invalid_credentials' });
    await dispatcher.onJobPolled(job, { outcome: 'invalid_credentials' });

    assert.equal(client.sends.length, 1);
    assert.match(client.sends[0].content, /Pausé tus búsquedas porque tus credenciales de UADE parecen vencidas/);
    assert.equal(getUser(db, 'user-1').lastPauseNotifiedReason, 'needs_credentials');

    updateAccountPauseState(db, 'user-1', { pauseReason: null, pauseUntil: null, backoffAttempt: 0 });
    await dispatcher.onJobPolled(job, { outcome: 'no_vacancies' });
    assert.equal(getUser(db, 'user-1').lastPauseNotifiedReason, null);

    updateAccountPauseState(db, 'user-1', {
      pauseReason: 'needs_credentials',
      pauseUntil: null,
      backoffAttempt: 0,
    });
    await dispatcher.onJobPolled(job, { outcome: 'invalid_credentials' });

    assert.equal(client.sends.length, 2);
  } finally {
    db.close();
  }
});

test('dispatcher throttles outbound sends in order', async () => {
  const db = createDatabase(':memory:');
  try {
    upsertUser(db, 'user-1');
    const job = createJob(db, { discordUserId: 'user-1', filtros: FILTROS, channelId: 'channel-1' });
    const client = createFakeClient();
    const dispatcher = createNotificationDispatcher({ client, db, delayMs: 5 });

    await dispatcher.onJobPolled(job, { outcome: 'found', vacancies: [VACANCY] });

    assert.equal(client.sends.length, 2);
    assert.ok(client.sends[1].at >= client.sends[0].at);
  } finally {
    db.close();
  }
});
