import assert from 'node:assert/strict';
import { test } from 'node:test';
import { createDatabase } from '../../db/database.js';
import { upsertCredentials } from '../../db/credentials.repository.js';
import { getJob, listJobsByUser } from '../../db/jobs.repository.js';
import { upsertUser } from '../../db/users.repository.js';
import { buscarCommand } from './buscar.js';
import { detenerCommand } from './detener.js';
import { estadoCommand } from './estado.js';
import { pausarCommand } from './pausar.js';
import { reanudarCommand } from './reanudar.js';
import { commands, commandsByName } from './index.js';

const VALID_OPTIONS = {
  materia: '3.1.050',
  turno: 'Mañana',
  ofrecimiento: 'curricular',
  dias: 'LU,MI',
  sedes_excluidas: 'Monserrat,Recoleta',
  etiqueta: 'Fisica II',
};

function createInteraction({ userId = 'user-1', channelId = 'channel-1', options = VALID_OPTIONS, focused = '' } = {}) {
  const calls = [];
  return {
    user: { id: userId },
    channelId,
    options: {
      getString(name) {
        return options[name] ?? null;
      },
      getFocused() {
        return focused;
      },
    },
    deferReply: async (payload) => calls.push(['deferReply', payload]),
    editReply: async (payload) => calls.push(['editReply', payload]),
    reply: async (payload) => calls.push(['reply', payload]),
    respond: async (payload) => calls.push(['respond', payload]),
    get calls() {
      return calls;
    },
  };
}

test('command builders expose search CRUD commands with required options and fixed choices', () => {
  assert.deepEqual(
    commands.map((command) => command.data.name),
    ['buscar', 'estado', 'detener', 'pausar', 'reanudar', 'credenciales'],
  );
  assert.equal(commandsByName.get('buscar'), buscarCommand);

  const buscarJson = buscarCommand.data.toJSON();
  assert.deepEqual(
    buscarJson.options.map((option) => option.name),
    ['materia', 'turno', 'ofrecimiento', 'dias', 'sedes_excluidas', 'etiqueta'],
  );

  const turno = buscarJson.options.find((option) => option.name === 'turno');
  assert.deepEqual(
    turno.choices.map((choice) => choice.value),
    ['Mañana', 'Tarde', 'Noche', 'Intensivo', 'Online'],
  );

  const ofrecimiento = buscarJson.options.find((option) => option.name === 'ofrecimiento');
  assert.deepEqual(
    ofrecimiento.choices.map((choice) => choice.value),
    ['curricular', 'optativa'],
  );

  for (const command of [detenerCommand, pausarCommand, reanudarCommand]) {
    const busqueda = command.data.toJSON().options.find((option) => option.name === 'busqueda');
    assert.equal(busqueda.autocomplete, true);
  }
});

test('/buscar validates locally, creates duplicate jobs, and never calls live search functions', async () => {
  const db = createDatabase(':memory:');
  try {
    const interaction = createInteraction();
    const liveSearch = async () => {
      throw new Error('live search must not run from /buscar');
    };

    await buscarCommand.execute(interaction, { db, pollOnce: liveSearch, runSearch: liveSearch });
    await buscarCommand.execute(createInteraction(), { db, pollOnce: liveSearch, runSearch: liveSearch });

    const jobs = listJobsByUser(db, 'user-1');
    assert.equal(jobs.length, 2);
    assert.equal(jobs[0].label, 'Fisica II');
    assert.equal(jobs[0].channelId, 'channel-1');
    assert.deepEqual(jobs[0].filtros, {
      materiaCodigo: '3.1.050',
      turno: 'Mañana',
      ofrecimiento: 'curricular',
      dias: ['LU', 'MI'],
      sedesExcluidas: ['Monserrat', 'Recoleta'],
    });
    assert.deepEqual(interaction.calls[0], ['deferReply', { ephemeral: true }]);
    assert.match(String(interaction.calls.at(-1)[1]), /Busqueda creada/i);
  } finally {
    db.close();
  }
});

test('/buscar enqueues an immediate poll after creating a job when credentials already exist', async () => {
  const db = createDatabase(':memory:');
  try {
    upsertUser(db, 'user-1');
    upsertCredentials(db, {
      discordUserId: 'user-1',
      ciphertext: 'ciphertext',
      iv: 'iv',
      authTag: 'auth-tag',
    });
    const enqueued = [];

    await buscarCommand.execute(createInteraction(), {
      db,
      onJobCreated: async (job) => {
        enqueued.push(job);
      },
    });

    assert.equal(enqueued.length, 1);
    assert.equal(enqueued[0].label, 'Fisica II');
    assert.equal(enqueued[0].discordUserId, 'user-1');
  } finally {
    db.close();
  }
});

test('/buscar does not enqueue an immediate poll if first-run credential onboarding fails', async () => {
  const db = createDatabase(':memory:');
  try {
    let enqueued = 0;

    await buscarCommand.execute(createInteraction(), {
      db,
      credentialOnboarding: async () => ({ ok: false, message: 'No pude abrirte DM.' }),
      onJobCreated: async () => {
        enqueued += 1;
      },
    });

    assert.equal(enqueued, 0);
  } finally {
    db.close();
  }
});

test('/buscar returns a Spanish validation error without DB writes for invalid materia format', async () => {
  const db = createDatabase(':memory:');
  try {
    const interaction = createInteraction({ options: { ...VALID_OPTIONS, materia: 'fisica' } });

    await buscarCommand.execute(interaction, { db });

    assert.equal(listJobsByUser(db, 'user-1').length, 0);
    assert.match(String(interaction.calls.at(-1)[1]), /codigo de materia/i);
  } finally {
    db.close();
  }
});

test('/estado lists only the caller jobs with filters, status, pause reason, and poll fields', async () => {
  const db = createDatabase(':memory:');
  try {
    await buscarCommand.execute(createInteraction({ userId: 'user-1' }), { db });
    await buscarCommand.execute(createInteraction({ userId: 'user-2', options: { ...VALID_OPTIONS, etiqueta: 'Otra' } }), { db });

    const [job] = listJobsByUser(db, 'user-1');
    db.prepare('UPDATE users SET pause_reason = ? WHERE discord_user_id = ?').run('needs_credentials', 'user-1');
    db.prepare('UPDATE jobs SET status = ?, last_polled_at = ?, last_outcome = ? WHERE id = ?').run(
      'paused_by_user',
      12345,
      'no_vacancies',
      job.id,
    );

    const interaction = createInteraction({ userId: 'user-1' });
    await estadoCommand.execute(interaction, { db });

    const reply = String(interaction.calls.at(-1)[1].content ?? interaction.calls.at(-1)[1]);
    assert.match(reply, /Fisica II/);
    assert.match(reply, /3\.1\.050/);
    assert.match(reply, /Mañana/);
    assert.match(reply, /LU, MI/);
    assert.match(reply, /Monserrat/);
    assert.match(reply, /pausada/i);
    assert.match(reply, /needs_credentials/);
    assert.match(reply, /Ultimo sondeo:/);
    assert.match(reply, /sin vacantes/);
    assert.doesNotMatch(reply, /12345/);
    assert.doesNotMatch(reply, /no_vacancies/);
    assert.doesNotMatch(reply, /Otra/);
  } finally {
    db.close();
  }
});

test('autocomplete returns at most 25 caller-owned job choices and filters by label', async () => {
  const db = createDatabase(':memory:');
  try {
    for (let index = 0; index < 30; index += 1) {
      await buscarCommand.execute(
        createInteraction({ options: { ...VALID_OPTIONS, materia: '3.1.050', etiqueta: `Fisica ${index}` } }),
        { db },
      );
    }
    await buscarCommand.execute(createInteraction({ userId: 'user-2', options: { ...VALID_OPTIONS, etiqueta: 'Fisica ajena' } }), { db });

    const interaction = createInteraction({ focused: 'Fisica' });
    await pausarCommand.autocomplete(interaction, { db });

    const choices = interaction.calls.at(-1)[1];
    assert.equal(choices.length, 25);
    assert.equal(choices.some((choice) => choice.name.includes('ajena')), false);
  } finally {
    db.close();
  }
});

test('/detener, /pausar, and /reanudar re-check ownership before mutation', async () => {
  const db = createDatabase(':memory:');
  try {
    upsertUser(db, 'user-1');
    await buscarCommand.execute(createInteraction({ userId: 'user-2' }), { db });
    const [foreignJob] = listJobsByUser(db, 'user-2');

    const foreignSelection = createInteraction({
      userId: 'user-1',
      options: { busqueda: String(foreignJob.id) },
    });

    await pausarCommand.execute(foreignSelection, { db });
    assert.equal(getJob(db, foreignJob.id).status, 'active');
    assert.match(String(foreignSelection.calls.at(-1)[1].content ?? foreignSelection.calls.at(-1)[1]), /No encontre/i);

    const owned = createInteraction({ userId: 'user-1' });
    await buscarCommand.execute(owned, { db });
    const [ownedJob] = listJobsByUser(db, 'user-1');

    await pausarCommand.execute(createInteraction({ userId: 'user-1', options: { busqueda: String(ownedJob.id) } }), { db });
    assert.equal(getJob(db, ownedJob.id).status, 'paused_by_user');

    await reanudarCommand.execute(createInteraction({ userId: 'user-1', options: { busqueda: String(ownedJob.id) } }), { db });
    assert.equal(getJob(db, ownedJob.id).status, 'active');

    await detenerCommand.execute(createInteraction({ userId: 'user-1', options: { busqueda: String(ownedJob.id) } }), { db });
    assert.equal(getJob(db, ownedJob.id), null);
  } finally {
    db.close();
  }
});
