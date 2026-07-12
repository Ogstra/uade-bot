import assert from 'node:assert/strict';
import { test } from 'node:test';
import { createDatabase } from '../db/database.js';
import { buscarCommand } from './commands/buscar.js';
import { createInteractionHandler } from './interactions.js';

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
          materia: '3.1.050',
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
