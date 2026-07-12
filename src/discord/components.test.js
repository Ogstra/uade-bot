import assert from 'node:assert/strict';
import { test } from 'node:test';
import { ButtonStyle } from 'discord.js';
import { createDatabase } from '../db/database.js';
import { createJob, getJob } from '../db/jobs.repository.js';
import { upsertUser } from '../db/users.repository.js';
import { buildVacancyActionRow, handleDetenerButton, isDetenerButton } from './components.js';

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

