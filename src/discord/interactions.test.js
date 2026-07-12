import assert from 'node:assert/strict';
import { test } from 'node:test';
import { createDatabase } from '../db/database.js';
import { createJob, getJob } from '../db/jobs.repository.js';
import { listCommandUsageByUser } from '../db/command-log.repository.js';
import { upsertUser } from '../db/users.repository.js';
import { buscarCommand } from './commands/buscar.js';
import { createInteractionHandler } from './interactions.js';

const FILTROS = {
  materiaCodigo: '3.1.050',
  ofrecimiento: 'curricular',
  turno: 'Mañana',
  dias: ['LU'],
  sedesExcluidas: [],
};

function createLogger() {
  return {
    errorCalls: [],
    error(payload, message) {
      this.errorCalls.push({ payload, message });
    },
  };
}

function createInteraction(overrides = {}) {
  const calls = [];
  return {
    commandName: 'buscar',
    guildId: 'guild-1',
    user: { id: 'user-1' },
    options: {
      getString(name) {
        const values = {
          cod_materia: '3.1.050',
          turno: 'Noche',
          ofrecimiento: 'curricular',
          dias: 'LU,MI',
          sedes_excluidas: null,
          etiqueta: null,
        };
        return values[name] ?? null;
      },
    },
    isChatInputCommand: () => true,
    isAutocomplete: () => false,
    deferReply: async (payload) => calls.push(['deferReply', payload]),
    editReply: async (payload) => calls.push(['editReply', payload]),
    reply: async (payload) => calls.push(['reply', payload]),
    respond: async (payload) => calls.push(['respond', payload]),
    get calls() {
      return calls;
    },
    ...overrides,
  };
}

test('dispatcher silently ignores guild chat commands from another server', async () => {
  const interaction = createInteraction({ guildId: 'other-guild' });
  const handler = createInteractionHandler({
    commandsByName: new Map([['buscar', buscarCommand]]),
    env: { DISCORD_GUILD_ID: 'guild-1' },
    logger: createLogger(),
  });

  await handler(interaction);

  assert.deepEqual(interaction.calls, []);
});

test('dispatcher silently ignores guild autocomplete from another server', async () => {
  const interaction = createInteraction({
    guildId: 'other-guild',
    isChatInputCommand: () => false,
    isAutocomplete: () => true,
  });
  const handler = createInteractionHandler({
    commandsByName: new Map([['buscar', buscarCommand]]),
    env: { DISCORD_GUILD_ID: 'guild-1' },
    logger: createLogger(),
  });

  await handler(interaction);

  assert.deepEqual(interaction.calls, []);
});

test('dispatcher allows DM chat commands without an extra membership check', async () => {
  const executed = [];
  const command = {
    ...buscarCommand,
    execute: async () => {
      executed.push('ok');
    },
  };
  const interaction = createInteraction({ guildId: null });
  const handler = createInteractionHandler({
    commandsByName: new Map([['buscar', command]]),
    env: { DISCORD_GUILD_ID: 'guild-1' },
    logger: createLogger(),
  });

  await handler(interaction);

  assert.deepEqual(executed, ['ok']);
});

test('dispatcher logs chat-input command usage to command_log', async () => {
  const db = createDatabase(':memory:');
  try {
    const command = { ...buscarCommand, execute: async () => {} };
    const interaction = createInteraction({ guildId: 'guild-1' });
    const handler = createInteractionHandler({
      commandsByName: new Map([['buscar', command]]),
      env: { DISCORD_GUILD_ID: 'guild-1' },
      logger: createLogger(),
      commandContext: { db },
    });

    await handler(interaction);

    const [entry] = listCommandUsageByUser(db, 'user-1');
    assert.equal(entry.commandName, 'buscar');
    assert.equal(entry.guildId, 'guild-1');
  } finally {
    db.close();
  }
});

test('dispatcher does not log autocomplete or button interactions to command_log', async () => {
  const db = createDatabase(':memory:');
  try {
    const command = { autocomplete: async (interaction) => interaction.respond([]) };
    const interaction = createInteraction({
      isChatInputCommand: () => false,
      isAutocomplete: () => true,
    });
    const handler = createInteractionHandler({
      commandsByName: new Map([['buscar', command]]),
      env: { DISCORD_GUILD_ID: 'guild-1' },
      logger: createLogger(),
      commandContext: { db },
    });

    await handler(interaction);

    assert.deepEqual(listCommandUsageByUser(db, 'user-1'), []);
  } finally {
    db.close();
  }
});

test('dispatcher passes commandContext to autocomplete handlers', async () => {
  const seenContexts = [];
  const commandContext = { db: 'db-context' };
  const command = {
    autocomplete: async (_interaction, context) => {
      seenContexts.push(context);
    },
  };
  const interaction = createInteraction({
    isChatInputCommand: () => false,
    isAutocomplete: () => true,
  });
  const handler = createInteractionHandler({
    commandsByName: new Map([['buscar', command]]),
    env: { DISCORD_GUILD_ID: 'guild-1' },
    logger: createLogger(),
    commandContext,
  });

  await handler(interaction);

  assert.deepEqual(seenContexts, [commandContext]);
});

test('/buscar defers before async work and finishes with editReply(), publicly by default', async () => {
  const events = [];
  const interaction = createInteraction({
    deferReply: async (payload) => events.push(['deferReply', payload]),
    editReply: async (payload) => events.push(['editReply', payload]),
  });
  const db = createDatabase(':memory:');

  await buscarCommand.execute(interaction, {
    db,
    credentialOnboarding: async () => ({ ok: true, message: 'ok' }),
  });

  assert.equal(events[0][0], 'deferReply');
  assert.deepEqual(events[0][1], { ephemeral: false });
  assert.equal(events.at(-1)[0], 'editReply');
});

test('/buscar defers ephemerally when ephemeralReplies: true is set', async () => {
  const events = [];
  const interaction = createInteraction({
    deferReply: async (payload) => events.push(['deferReply', payload]),
    editReply: async (payload) => events.push(['editReply', payload]),
  });
  const db = createDatabase(':memory:');

  await buscarCommand.execute(interaction, {
    db,
    credentialOnboarding: async () => ({ ok: true, message: 'ok' }),
    ephemeralReplies: true,
  });

  assert.deepEqual(events[0], ['deferReply', { ephemeral: true }]);
});

test('dispatcher routes a "Detener busqueda" button click to handleDetenerButton', async () => {
  const db = createDatabase(':memory:');
  try {
    upsertUser(db, 'user-1');
    const job = createJob(db, { discordUserId: 'user-1', filtros: FILTROS, label: 'Fisica II' });

    const interaction = createInteraction({
      commandName: undefined,
      isChatInputCommand: () => false,
      isAutocomplete: () => false,
      isButton: () => true,
      customId: `detener_job:${job.id}`,
    });
    const handler = createInteractionHandler({
      commandsByName: new Map([['buscar', buscarCommand]]),
      env: { DISCORD_GUILD_ID: 'guild-1' },
      logger: createLogger(),
      commandContext: { db },
    });

    await handler(interaction);

    assert.equal(getJob(db, job.id), null);
    assert.equal(interaction.calls[0][0], 'reply');
    assert.match(interaction.calls[0][1].content, /Busqueda detenida/);
  } finally {
    db.close();
  }
});

test('dispatcher silently ignores button clicks from another server', async () => {
  const db = createDatabase(':memory:');
  try {
    upsertUser(db, 'user-1');
    const job = createJob(db, { discordUserId: 'user-1', filtros: FILTROS, label: 'Fisica II' });

    const interaction = createInteraction({
      guildId: 'other-guild',
      commandName: undefined,
      isChatInputCommand: () => false,
      isAutocomplete: () => false,
      isButton: () => true,
      customId: `detener_job:${job.id}`,
    });
    const handler = createInteractionHandler({
      commandsByName: new Map([['buscar', buscarCommand]]),
      env: { DISCORD_GUILD_ID: 'guild-1' },
      logger: createLogger(),
      commandContext: { db },
    });

    await handler(interaction);

    assert.deepEqual(interaction.calls, []);
    assert.notEqual(getJob(db, job.id), null);
  } finally {
    db.close();
  }
});

test('dispatcher never throws when both the command handler and the fallback error reply fail', async () => {
  const db = createDatabase(':memory:');
  try {
    const command = {
      execute: async () => {
        throw new Error('boom');
      },
    };
    const errorLogger = createLogger();
    const interaction = createInteraction({
      reply: async () => {
        // Mirrors a real "Interaction has already been acknowledged" (40060)
        // failure: our own local reply attempt errors even though Discord's
        // server-side state may already consider the interaction handled.
        throw new Error('Interaction has already been acknowledged.');
      },
    });
    const handler = createInteractionHandler({
      commandsByName: new Map([['buscar', command]]),
      env: { DISCORD_GUILD_ID: 'guild-1' },
      logger: errorLogger,
      commandContext: { db },
    });

    await assert.doesNotReject(() => handler(interaction));
    assert.ok(errorLogger.errorCalls.length >= 1);
  } finally {
    db.close();
  }
});

function fakePermissions(hasAdmin) {
  return { has: () => hasAdmin };
}

test('dispatcher rejects a non-admin invoking an adminOnly command with a not-authorized reply', async () => {
  const executed = [];
  const command = { adminOnly: true, execute: async () => executed.push('ran') };
  const interaction = createInteraction({ commandName: 'admin-stats', memberPermissions: fakePermissions(false) });
  const handler = createInteractionHandler({
    commandsByName: new Map([['admin-stats', command]]),
    env: { DISCORD_GUILD_ID: 'guild-1' },
    logger: createLogger(),
  });

  await handler(interaction);

  assert.deepEqual(executed, []);
  assert.equal(interaction.calls[0][0], 'reply');
  assert.match(interaction.calls[0][1].content, /solo para administradores/i);
});

test('dispatcher lets an admin invoke an adminOnly command', async () => {
  const executed = [];
  const command = {
    adminOnly: true,
    execute: async (i) => {
      executed.push('ran');
      await i.reply({ content: 'ok', ephemeral: true });
    },
  };
  const interaction = createInteraction({ commandName: 'admin-stats', memberPermissions: fakePermissions(true) });
  const db = createDatabase(':memory:');
  try {
    const handler = createInteractionHandler({
      commandsByName: new Map([['admin-stats', command]]),
      env: { DISCORD_GUILD_ID: 'guild-1' },
      logger: createLogger(),
      commandContext: { db },
    });

    await handler(interaction);

    assert.deepEqual(executed, ['ran']);
  } finally {
    db.close();
  }
});

test('dispatcher silently empties autocomplete for a non-admin on an adminOnly command, without leaking choices', async () => {
  const command = {
    adminOnly: true,
    autocomplete: async (i) => i.respond([{ name: 'secret-job-of-another-user', value: '1' }]),
  };
  const interaction = createInteraction({
    commandName: 'admin-detener',
    isChatInputCommand: () => false,
    isAutocomplete: () => true,
    memberPermissions: fakePermissions(false),
  });
  const handler = createInteractionHandler({
    commandsByName: new Map([['admin-detener', command]]),
    env: { DISCORD_GUILD_ID: 'guild-1' },
    logger: createLogger(),
  });

  await handler(interaction);

  assert.deepEqual(interaction.calls, [['respond', []]]);
});
