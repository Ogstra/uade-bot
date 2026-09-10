import assert from 'node:assert/strict';
import { test } from 'node:test';
import { MessageFlags } from 'discord.js';
import { createDatabase } from '../../db/database.js';
import { createJob, getJob, listAllJobs } from '../../db/jobs.repository.js';
import { logCommandUsage } from '../../db/command-log.repository.js';
import { upsertUser } from '../../db/users.repository.js';
import { adminEstadoCommand } from './admin-estado.js';
import { adminDetenerCommand } from './admin-detener.js';
import { adminPausarCommand } from './admin-pausar.js';
import { adminReanudarCommand } from './admin-reanudar.js';
import { adminStatsCommand } from './admin-stats.js';
import { adminUserStatsCommand } from './admin-user-stats.js';
import { autocompleteAllJobs, autocompleteAllUsers, autocompleteJobOwners } from './job-selection.js';

const FILTROS = {
  materiaCodigo: '3.1.050',
  ofrecimiento: 'curricular',
  turno: 'Mañana',
  dias: ['LU'],
  sedesExcluidas: [],
};

const USERNAMES = { 'user-1': 'alice', 'user-2': 'bob' };

function fakeClient() {
  return {
    users: {
      fetch: async (id) => {
        if (!(id in USERNAMES)) {
          throw new Error('unknown user');
        }
        return { username: USERNAMES[id], globalName: null };
      },
    },
    guilds: {
      cache: new Map([['guild-1', { name: 'Los Pibes de UADE' }]]),
    },
  };
}

function createInteraction({ userId = 'admin-1', options = {}, focused = '', client = fakeClient() } = {}) {
  const calls = [];
  return {
    user: { id: userId },
    client,
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
  assert.equal(adminPausarCommand.adminOnly, true);
  assert.equal(adminReanudarCommand.adminOnly, true);
  assert.equal(adminStatsCommand.adminOnly, true);
  assert.equal(adminUserStatsCommand.adminOnly, true);
});

test('/admin-estado shows the resolved server name and a channel mention per job', async () => {
  const db = createDatabase(':memory:');
  try {
    upsertUser(db, 'user-1');
    const job = createJob(db, {
      discordUserId: 'user-1',
      filtros: FILTROS,
      label: 'Fisica II',
      channelId: 'channel-1',
      guildId: 'guild-1',
    });

    const interaction = createInteraction();
    await adminEstadoCommand.execute(interaction, { db });

    assert.match(interaction.calls[0][1].content, new RegExp(`#${job.id}.*Los Pibes de UADE.*<#channel-1>`));
  } finally {
    db.close();
  }
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
    assert.equal(reply.flags, MessageFlags.Ephemeral);
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

test('/admin-estado filters to a single account when the usuario option is set', async () => {
  const db = createDatabase(':memory:');
  try {
    upsertUser(db, 'user-1');
    upsertUser(db, 'user-2');
    const jobA = createJob(db, { discordUserId: 'user-1', filtros: FILTROS, label: 'Fisica II' });
    const jobB = createJob(db, { discordUserId: 'user-2', filtros: { ...FILTROS, materiaCodigo: '3.4.219' } });

    const interaction = createInteraction({ options: { usuario: 'user-1' } });
    await adminEstadoCommand.execute(interaction, { db });

    const content = interaction.calls[0][1].content;
    assert.match(content, new RegExp(`#${jobA.id}`));
    assert.doesNotMatch(content, new RegExp(`#${jobB.id}`));
  } finally {
    db.close();
  }
});

test('autocompleteJobOwners lists only accounts that currently have a job, with a resolved display name', async () => {
  const db = createDatabase(':memory:');
  try {
    upsertUser(db, 'user-1');
    upsertUser(db, 'user-2');
    createJob(db, { discordUserId: 'user-1', filtros: FILTROS });

    const interaction = createInteraction();
    await autocompleteJobOwners(interaction, { db });

    const choices = interaction.calls[0][1];
    assert.equal(choices.length, 1);
    assert.match(choices[0].name, /alice/);
    assert.equal(choices[0].value, 'user-1');
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
    assert.equal(interaction.calls[0][1].flags, MessageFlags.Ephemeral);
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

test('/admin-pausar pauses another account\'s active job', async () => {
  const db = createDatabase(':memory:');
  try {
    upsertUser(db, 'user-1');
    const job = createJob(db, { discordUserId: 'user-1', filtros: FILTROS, label: 'Fisica II' });

    const interaction = createInteraction({ options: { busqueda: String(job.id) } });
    await adminPausarCommand.execute(interaction, { db });

    assert.equal(getJob(db, job.id).status, 'paused_by_user');
    assert.match(interaction.calls[0][1].content, /Busqueda pausada \(admin\)/);
    assert.match(interaction.calls[0][1].content, /user-1/);
  } finally {
    db.close();
  }
});

test('/admin-pausar replies not-found for a bogus job id', async () => {
  const db = createDatabase(':memory:');
  try {
    const interaction = createInteraction({ options: { busqueda: '9999' } });
    await adminPausarCommand.execute(interaction, { db });

    assert.match(interaction.calls[0][1].content, /No encontre ninguna busqueda/i);
  } finally {
    db.close();
  }
});

test('/admin-reanudar resumes another account\'s paused job and triggers an immediate poll', async () => {
  const db = createDatabase(':memory:');
  try {
    upsertUser(db, 'user-1');
    const job = createJob(db, { discordUserId: 'user-1', filtros: FILTROS, label: 'Fisica II' });
    db.prepare("UPDATE jobs SET status = 'paused_by_user' WHERE id = ?").run(job.id);

    const polled = [];
    const interaction = createInteraction({ options: { busqueda: String(job.id) } });
    await adminReanudarCommand.execute(interaction, {
      db,
      onJobResumed: async (resumedJob) => polled.push(resumedJob.id),
    });

    assert.equal(getJob(db, job.id).status, 'active');
    assert.match(interaction.calls[0][1].content, /Busqueda reanudada \(admin\)/);
    assert.match(interaction.calls[0][1].content, /user-1/);
    assert.deepEqual(polled, [job.id]);
  } finally {
    db.close();
  }
});

test('/admin-reanudar replies not-found for a bogus job id', async () => {
  const db = createDatabase(':memory:');
  try {
    const interaction = createInteraction({ options: { busqueda: '9999' } });
    await adminReanudarCommand.execute(interaction, { db });

    assert.match(interaction.calls[0][1].content, /No encontre ninguna busqueda/i);
  } finally {
    db.close();
  }
});

test('autocompleteAllJobs surfaces jobs across accounts with the job id and a resolved owner name', async () => {
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
    assert.match(choices[0].name, new RegExp(`#${jobA.id} · alice`));
    assert.match(choices[1].name, new RegExp(`#${jobB.id} · bob`));
    assert.equal(choices[0].value, String(jobA.id));
    assert.equal(choices[1].value, String(jobB.id));
  } finally {
    db.close();
  }
});

test('autocompleteAllJobs falls back to the raw account id when the user fetch fails', async () => {
  const db = createDatabase(':memory:');
  try {
    upsertUser(db, 'ghost-user');
    createJob(db, { discordUserId: 'ghost-user', filtros: FILTROS });

    const interaction = createInteraction();
    await autocompleteAllJobs(interaction, { db });

    assert.match(interaction.calls[0][1][0].name, /ghost-user/);
  } finally {
    db.close();
  }
});

test('listAllJobs still backs /admin-estado with no filter (sanity check for the usuario option default)', async () => {
  const db = createDatabase(':memory:');
  try {
    upsertUser(db, 'user-1');
    createJob(db, { discordUserId: 'user-1', filtros: FILTROS });
    assert.equal(listAllJobs(db).length, 1);
  } finally {
    db.close();
  }
});

test('/admin-user-stats reports one account\'s jobs, pause state, and command usage', async () => {
  const db = createDatabase(':memory:');
  try {
    upsertUser(db, 'user-1');
    const job = createJob(db, { discordUserId: 'user-1', filtros: FILTROS, label: 'Fisica II' });
    logCommandUsage(db, { discordUserId: 'user-1', commandName: 'buscar', guildId: 'guild-1' });
    logCommandUsage(db, { discordUserId: 'user-1', commandName: 'buscar', guildId: 'guild-1' });
    logCommandUsage(db, { discordUserId: 'user-2', commandName: 'estado', guildId: 'guild-1' });

    const interaction = createInteraction({ options: { usuario: 'user-1' } });
    await adminUserStatsCommand.execute(interaction, { db });

    const content = interaction.calls[0][1].content;
    assert.match(content, /Estadisticas de alice/);
    assert.match(content, /Estado de la cuenta:\*\* activa/);
    assert.match(content, /Busquedas activas:\*\* 1/);
    assert.match(content, /Comandos ejecutados \(total\):\*\* 2/);
    assert.match(content, /`\/buscar`: 2/);
    assert.match(content, new RegExp(`#${job.id}`));
    assert.equal(interaction.calls[0][1].flags, MessageFlags.Ephemeral);
  } finally {
    db.close();
  }
});

test('/admin-user-stats shows the pause reason for a paused account', async () => {
  const db = createDatabase(':memory:');
  try {
    upsertUser(db, 'user-2');
    db.prepare("UPDATE users SET pause_reason = 'needs_credentials' WHERE discord_user_id = 'user-2'").run();

    const interaction = createInteraction({ options: { usuario: 'user-2' } });
    await adminUserStatsCommand.execute(interaction, { db });

    assert.match(interaction.calls[0][1].content, /pausada \(needs_credentials\)/);
  } finally {
    db.close();
  }
});

test('/admin-user-stats replies not-found for an unregistered account', async () => {
  const db = createDatabase(':memory:');
  try {
    const interaction = createInteraction({ options: { usuario: 'nobody' } });
    await adminUserStatsCommand.execute(interaction, { db });

    assert.match(interaction.calls[0][1].content, /no esta registrada/i);
  } finally {
    db.close();
  }
});

test('autocompleteAllUsers lists every registered account, even ones without jobs', async () => {
  const db = createDatabase(':memory:');
  try {
    upsertUser(db, 'user-1');
    upsertUser(db, 'user-2');

    const interaction = createInteraction();
    await autocompleteAllUsers(interaction, { db });

    const choices = interaction.calls[0][1];
    assert.equal(choices.length, 2);
    assert.deepEqual(choices.map((c) => c.value).sort(), ['user-1', 'user-2']);
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
