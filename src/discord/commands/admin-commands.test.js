import assert from 'node:assert/strict';
import { test } from 'node:test';
import { createDatabase } from '../../db/database.js';
import { createJob, getJob } from '../../db/jobs.repository.js';
import { logCommandUsage } from '../../db/command-log.repository.js';
import { upsertUser } from '../../db/users.repository.js';
import { adminEstadoCommand } from './admin-estado.js';
import { adminDetenerCommand } from './admin-detener.js';
import { adminStatsCommand } from './admin-stats.js';
import { autocompleteAllJobs } from './job-selection.js';

const FILTROS = {
  materiaCodigo: '3.1.050',
  ofrecimiento: 'curricular',
  turno: 'Mañana',
  dias: ['LU'],
  sedesExcluidas: [],
};

function createInteraction({ userId = 'admin-1', options = {}, focused = '' } = {}) {
  const calls = [];
  return {
    user: { id: userId },
    options: {
      getString: (name) => options[name] ?? null,
      getFocused: () => focused,
    },
    reply: async (payload) => calls.push(['reply', payload]),
    respond: async (payload) => calls.push(['respond', payload]),
    get calls() {
      return calls;
    },
  };
}

test('all admin commands are flagged adminOnly for the central interactions.js gate', () => {
  assert.equal(adminEstadoCommand.adminOnly, true);
  assert.equal(adminDetenerCommand.adminOnly, true);
  assert.equal(adminStatsCommand.adminOnly, true);
});

test('/admin-estado lists jobs from every account, not just the caller', async () => {
  const db = createDatabase(':memory:');
  try {
    upsertUser(db, 'user-1');
    upsertUser(db, 'user-2');
    const jobA = createJob(db, { discordUserId: 'user-1', filtros: FILTROS, label: 'Fisica II' });
    const jobB = createJob(db, { discordUserId: 'user-2', filtros: { ...FILTROS, materiaCodigo: '3.4.219' } });

    const interaction = createInteraction();
    await adminEstadoCommand.execute(interaction, { db });

    const reply = interaction.calls[0][1];
    assert.equal(reply.ephemeral, true);
    assert.match(reply.content, new RegExp(`#${jobA.id}.*user-1`));
    assert.match(reply.content, new RegExp(`#${jobB.id}.*user-2`));
  } finally {
    db.close();
  }
});

test('/admin-estado shows a friendly empty state when nobody has any jobs', async () => {
  const db = createDatabase(':memory:');
  try {
    const interaction = createInteraction();
    await adminEstadoCommand.execute(interaction, { db });

    assert.match(interaction.calls[0][1].content, /No hay busquedas/i);
  } finally {
    db.close();
  }
});

test('/admin-detener deletes another account\'s job and confirms which account it belonged to', async () => {
  const db = createDatabase(':memory:');
  try {
    upsertUser(db, 'user-1');
    const job = createJob(db, { discordUserId: 'user-1', filtros: FILTROS, label: 'Fisica II' });

    const interaction = createInteraction({ userId: 'admin-1', options: { busqueda: String(job.id) } });
    await adminDetenerCommand.execute(interaction, { db });

    assert.equal(getJob(db, job.id), null);
    assert.match(interaction.calls[0][1].content, /Busqueda detenida \(admin\)/);
    assert.match(interaction.calls[0][1].content, /user-1/);
    assert.equal(interaction.calls[0][1].ephemeral, true);
  } finally {
    db.close();
  }
});

test('/admin-detener replies not-found for a bogus or already-gone job id', async () => {
  const db = createDatabase(':memory:');
  try {
    const interaction = createInteraction({ options: { busqueda: '9999' } });
    await adminDetenerCommand.execute(interaction, { db });

    assert.match(interaction.calls[0][1].content, /No encontre ninguna busqueda/i);
  } finally {
    db.close();
  }
});

test('autocompleteAllJobs surfaces jobs across accounts with the job id and owner distinguishable', async () => {
  const db = createDatabase(':memory:');
  try {
    upsertUser(db, 'user-1');
    upsertUser(db, 'user-2');
    const jobA = createJob(db, { discordUserId: 'user-1', filtros: FILTROS, label: 'Duplicada' });
    const jobB = createJob(db, { discordUserId: 'user-2', filtros: FILTROS, label: 'Duplicada' });

    const interaction = createInteraction({ focused: 'duplicada' });
    await autocompleteAllJobs(interaction, { db });

    const choices = interaction.calls[0][1];
    assert.equal(choices.length, 2);
    assert.match(choices[0].name, new RegExp(`#${jobA.id}`));
    assert.match(choices[1].name, new RegExp(`#${jobB.id}`));
    assert.equal(choices[0].value, String(jobA.id));
    assert.equal(choices[1].value, String(jobB.id));
  } finally {
    db.close();
  }
});

test('/admin-stats reports user/job counts, paused accounts, materia cache size, and command usage', async () => {
  const db = createDatabase(':memory:');
  try {
    upsertUser(db, 'user-1');
    upsertUser(db, 'user-2');
    createJob(db, { discordUserId: 'user-1', filtros: FILTROS });
    const paused = createJob(db, { discordUserId: 'user-2', filtros: FILTROS });
    db.prepare("UPDATE jobs SET status = 'paused_by_user' WHERE id = ?").run(paused.id);
    db.prepare("UPDATE users SET pause_reason = 'needs_credentials' WHERE discord_user_id = 'user-2'").run();
    logCommandUsage(db, { discordUserId: 'user-1', commandName: 'buscar', guildId: 'guild-1' });
    logCommandUsage(db, { discordUserId: 'user-1', commandName: 'estado', guildId: 'guild-1' });

    const interaction = createInteraction();
    await adminStatsCommand.execute(interaction, { db });

    const content = interaction.calls[0][1].content;
    assert.match(content, /Usuarios registrados:\*\* 2/);
    assert.match(content, /Busquedas activas:\*\* 1/);
    assert.match(content, /Busquedas pausadas.*:\*\* 1/);
    assert.match(content, /Cuentas pausadas.*:\*\* 1/);
    assert.match(content, /user-2/);
    assert.match(content, /Comandos ejecutados \(total\):\*\* 2/);
    assert.match(content, /`\/buscar`: 1/);
  } finally {
    db.close();
  }
});
