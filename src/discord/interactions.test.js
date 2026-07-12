import assert from 'node:assert/strict';
import { test } from 'node:test';
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
      getString() {
        return null;
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

test('/buscar defers ephemerally before async work and finishes with editReply()', async () => {
  const events = [];
  const interaction = createInteraction({
    deferReply: async (payload) => events.push(['deferReply', payload]),
    editReply: async (payload) => events.push(['editReply', payload]),
  });

  await buscarCommand.execute(interaction);

  assert.equal(events[0][0], 'deferReply');
  assert.deepEqual(events[0][1], { ephemeral: true });
  assert.equal(events.at(-1)[0], 'editReply');
});
