import assert from 'node:assert/strict';
import { test } from 'node:test';
import { ButtonStyle } from 'discord.js';
import { createDatabase } from '../db/database.js';
import { createJob, getJob } from '../db/jobs.repository.js';
import { upsertUser } from '../db/users.repository.js';
import {
  buildEstadoActionRows,
  buildVacancyActionRow,
  handleDetenerButton,
  handleTogglePauseButton,
  isDetenerButton,
  isTogglePauseButton,
} from './components.js';

const FILTROS = {
  materiaCodigo: '3.1.050',
  ofrecimiento: 'curricular',
  turno: 'Mañana',
  dias: ['LU'],
  sedesExcluidas: [],
};

function createButtonInteraction({ userId = 'user-1', customId, channelId = 'dm-channel' } = {}) {
  const calls = [];
  const channelSends = [];
  return {
    user: { id: userId },
    customId,
    channelId,
    isButton: () => true,
    client: {
      channels: {
        async fetch(id) {
          return {
            async send(content) {
              channelSends.push({ id, content });
            },
          };
        },
      },
    },
    reply: async (payload) => calls.push(['reply', payload]),
    get calls() {
      return calls;
    },
    get channelSends() {
      return channelSends;
    },
  };
}

test('buildVacancyActionRow attaches a Danger "Detener busqueda" button encoding the job id', () => {
  const row = buildVacancyActionRow(42);
  const json = row.toJSON();

  assert.equal(json.components.length, 1);
  assert.equal(json.components[0].custom_id, 'detener_job:42');
  assert.equal(json.components[0].style, ButtonStyle.Danger);
  assert.match(json.components[0].label, /Detener busqueda/);
});

test('isDetenerButton recognizes only well-formed detener_job custom ids on button interactions', () => {
  assert.equal(isDetenerButton(createButtonInteraction({ customId: 'detener_job:5' })), true);
  assert.equal(isDetenerButton(createButtonInteraction({ customId: 'detener_job:abc' })), false);
  assert.equal(isDetenerButton(createButtonInteraction({ customId: 'otro:5' })), false);
  assert.equal(isDetenerButton({ isButton: () => false, customId: 'detener_job:5' }), false);
});

test('handleDetenerButton deletes the job and confirms when the clicker owns it', async () => {
  const db = createDatabase(':memory:');
  try {
    upsertUser(db, 'user-1');
    const job = createJob(db, { discordUserId: 'user-1', filtros: FILTROS, label: 'Fisica II' });
    const interaction = createButtonInteraction({ userId: 'user-1', customId: `detener_job:${job.id}` });

    await handleDetenerButton(interaction, { db });

    assert.equal(getJob(db, job.id), null);
    assert.equal(interaction.calls[0][0], 'reply');
    assert.match(interaction.calls[0][1].content, /Busqueda detenida/);
    assert.equal(interaction.calls[0][1].ephemeral, true);
    assert.equal(interaction.channelSends.length, 0);
  } finally {
    db.close();
  }
});

test('handleDetenerButton echoes the confirmation to the original channel when stopped from the DM button', async () => {
  const db = createDatabase(':memory:');
  try {
    upsertUser(db, 'user-1');
    const job = createJob(db, {
      discordUserId: 'user-1',
      filtros: FILTROS,
      label: 'Fisica II',
      channelId: 'original-channel',
    });
    const interaction = createButtonInteraction({
      userId: 'user-1',
      customId: `detener_job:${job.id}`,
      channelId: 'dm-channel',
    });

    await handleDetenerButton(interaction, { db });

    assert.equal(interaction.channelSends.length, 1);
    assert.equal(interaction.channelSends[0].id, 'original-channel');
    assert.match(interaction.channelSends[0].content, /Busqueda detenida/);
  } finally {
    db.close();
  }
});

test('handleDetenerButton does not double-post when clicked from the search\'s own channel', async () => {
  const db = createDatabase(':memory:');
  try {
    upsertUser(db, 'user-1');
    const job = createJob(db, {
      discordUserId: 'user-1',
      filtros: FILTROS,
      label: 'Fisica II',
      channelId: 'original-channel',
    });
    const interaction = createButtonInteraction({
      userId: 'user-1',
      customId: `detener_job:${job.id}`,
      channelId: 'original-channel',
    });

    await handleDetenerButton(interaction, { db });

    assert.equal(interaction.channelSends.length, 0);
  } finally {
    db.close();
  }
});

test('handleDetenerButton treats a click from a non-owner as not-found and does not delete the job', async () => {
  const db = createDatabase(':memory:');
  try {
    upsertUser(db, 'user-1');
    const job = createJob(db, { discordUserId: 'user-1', filtros: FILTROS, label: 'Fisica II' });
    const interaction = createButtonInteraction({ userId: 'user-2', customId: `detener_job:${job.id}` });

    await handleDetenerButton(interaction, { db });

    assert.notEqual(getJob(db, job.id), null);
    assert.match(interaction.calls[0][1].content, /No encontre/i);
  } finally {
    db.close();
  }
});

test('handleDetenerButton replies not-found for an already-deleted or bogus job id', async () => {
  const db = createDatabase(':memory:');
  try {
    const interaction = createButtonInteraction({ userId: 'user-1', customId: 'detener_job:9999' });

    await handleDetenerButton(interaction, { db });

    assert.match(interaction.calls[0][1].content, /No encontre/i);
  } finally {
    db.close();
  }
});

test('isTogglePauseButton recognizes only well-formed toggle_pause_job custom ids', () => {
  assert.equal(isTogglePauseButton(createButtonInteraction({ customId: 'toggle_pause_job:5' })), true);
  assert.equal(isTogglePauseButton(createButtonInteraction({ customId: 'toggle_pause_job:abc' })), false);
  assert.equal(isTogglePauseButton(createButtonInteraction({ customId: 'detener_job:5' })), false);
  assert.equal(isDetenerButton(createButtonInteraction({ customId: 'toggle_pause_job:5' })), false);
});

test('buildEstadoActionRows packs 2 jobs per row, 2 buttons per job, labeled by status', () => {
  const jobs = [
    { id: 1, label: 'Fisica II', status: 'active' },
    { id: 2, label: 'Quimica', status: 'paused_by_user' },
    { id: 3, label: 'Analisis', status: 'active' },
  ];

  const rows = buildEstadoActionRows(jobs).map((row) => row.toJSON());

  assert.equal(rows.length, 2);
  assert.equal(rows[0].components.length, 4);
  assert.equal(rows[1].components.length, 2);

  const [job1Toggle, job1Detener, job2Toggle, job2Detener] = rows[0].components;
  assert.equal(job1Toggle.custom_id, 'toggle_pause_job:1');
  assert.match(job1Toggle.label, /^Pausar Fisica II$/);
  assert.equal(job1Toggle.style, ButtonStyle.Secondary);
  assert.equal(job1Detener.custom_id, 'detener_job:1');
  assert.equal(job1Detener.style, ButtonStyle.Danger);

  assert.equal(job2Toggle.custom_id, 'toggle_pause_job:2');
  assert.match(job2Toggle.label, /^Reanudar Quimica$/);

  const [job3Toggle] = rows[1].components;
  assert.equal(job3Toggle.custom_id, 'toggle_pause_job:3');
});

test('buildEstadoActionRows truncates long labels to Discord\'s 80-char button limit', () => {
  const jobs = [{ id: 1, label: 'X'.repeat(90), status: 'active' }];

  const [row] = buildEstadoActionRows(jobs).map((r) => r.toJSON());

  assert.ok(row.components[0].label.length <= 80);
  assert.ok(row.components[1].label.length <= 80);
});

test('handleTogglePauseButton pauses an active job without enqueuing a poll', async () => {
  const db = createDatabase(':memory:');
  try {
    upsertUser(db, 'user-1');
    const job = createJob(db, { discordUserId: 'user-1', filtros: FILTROS, label: 'Fisica II' });
    const interaction = createButtonInteraction({ userId: 'user-1', customId: `toggle_pause_job:${job.id}` });
    let enqueued = 0;

    await handleTogglePauseButton(interaction, { db, onJobResumed: async () => { enqueued += 1; } });

    assert.equal(getJob(db, job.id).status, 'paused_by_user');
    assert.match(interaction.calls[0][1].content, /Busqueda pausada/);
    assert.equal(enqueued, 0);
  } finally {
    db.close();
  }
});

test('handleTogglePauseButton resumes a paused job and enqueues an immediate poll', async () => {
  const db = createDatabase(':memory:');
  try {
    upsertUser(db, 'user-1');
    const job = createJob(db, { discordUserId: 'user-1', filtros: FILTROS, label: 'Fisica II' });
    db.prepare('UPDATE jobs SET status = ? WHERE id = ?').run('paused_by_user', job.id);
    const interaction = createButtonInteraction({ userId: 'user-1', customId: `toggle_pause_job:${job.id}` });
    const enqueued = [];

    await handleTogglePauseButton(interaction, { db, onJobResumed: async (resumedJob) => { enqueued.push(resumedJob); } });

    assert.equal(getJob(db, job.id).status, 'active');
    assert.match(interaction.calls[0][1].content, /Busqueda reanudada/);
    assert.equal(enqueued.length, 1);
    assert.equal(enqueued[0].id, job.id);
  } finally {
    db.close();
  }
});

test('handleTogglePauseButton treats a click from a non-owner as not-found and does not change status', async () => {
  const db = createDatabase(':memory:');
  try {
    upsertUser(db, 'user-1');
    const job = createJob(db, { discordUserId: 'user-1', filtros: FILTROS, label: 'Fisica II' });
    const interaction = createButtonInteraction({ userId: 'user-2', customId: `toggle_pause_job:${job.id}` });

    await handleTogglePauseButton(interaction, { db });

    assert.equal(getJob(db, job.id).status, 'active');
    assert.match(interaction.calls[0][1].content, /No encontre/i);
  } finally {
    db.close();
  }
});
