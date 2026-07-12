import assert from 'node:assert/strict';
import { test } from 'node:test';
import { Routes } from 'discord.js';
import { commands } from './commands/index.js';
import { registerCommands } from './register-commands.js';

test('registerCommands registers all commands only to the configured guild route', async () => {
  const calls = [];
  const rest = {
    async put(route, body) {
      calls.push({ route, body });
    },
  };
  const env = {
    DISCORD_CLIENT_ID: 'client-1',
    DISCORD_GUILD_ID: 'guild-1',
  };

  await registerCommands({ rest, env, commands });

  assert.equal(calls.length, 1);
  assert.equal(calls[0].route, Routes.applicationGuildCommands('client-1', 'guild-1'));
  assert.deepEqual(
    calls[0].body.body.map((command) => command.name),
    ['buscar', 'estado', 'detener', 'pausar', 'reanudar', 'credenciales', 'admin-estado', 'admin-detener', 'admin-stats'],
  );
});
