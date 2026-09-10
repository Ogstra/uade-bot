import assert from 'node:assert/strict';
import { test } from 'node:test';
import { Routes } from 'discord.js';
import { commands } from './commands/index.js';
import { registerCommands } from './register-commands.js';

test('registerCommands registers all commands to the application-global route', async () => {
  const calls = [];
  const rest = {
    async put(route, body) {
      calls.push({ route, body });
    },
  };
  const env = {
    DISCORD_CLIENT_ID: 'client-1',
  };

  await registerCommands({ rest, env, commands });

  assert.equal(calls.length, 1);
  assert.equal(calls[0].route, Routes.applicationCommands('client-1'));
  assert.deepEqual(
    calls[0].body.body.map((command) => command.name),
    [
      'buscar',
      'estado',
      'detener',
      'pausar',
      'reanudar',
      'credenciales',
      'admin-estado',
      'admin-detener',
      'admin-pausar',
      'admin-reanudar',
      'admin-stats',
      'admin-user-stats',
    ],
  );
});
