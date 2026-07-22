import assert from 'node:assert/strict';
import { test } from 'node:test';
import { Routes } from 'discord.js';
import { buscarCommand } from './buscar.js';
import { commands, commandsByName } from './index.js';
import { registerCommands } from '../register-commands.js';

test('/buscar exports a SlashCommandBuilder with the expected options and fixed choices', () => {
  const json = buscarCommand.data.toJSON();

  assert.equal(json.name, 'buscar');

  const optionNames = json.options.map((option) => option.name);
  assert.deepEqual(optionNames, [
    'cod_materia',
    'turno',
    'ofrecimiento',
    'dias',
    'sedes_excluidas',
    'etiqueta',
  ]);

  const turno = json.options.find((option) => option.name === 'turno');
  assert.deepEqual(
    turno.choices.map((choice) => choice.name),
    ['Mañana', 'Tarde', 'Noche', 'Intensivo', 'Online'],
  );

  const ofrecimiento = json.options.find((option) => option.name === 'ofrecimiento');
  assert.deepEqual(
    ofrecimiento.choices.map((choice) => choice.value),
    ['curricular', 'optativa'],
  );
});

test('commands index exposes /buscar by name', () => {
  assert.equal(commands.length, 12);
  assert.equal(commandsByName.get('buscar'), buscarCommand);
});

test('registerCommands() registers global commands', async () => {
  const calls = [];
  const rest = {
    put(route, body) {
      calls.push({ route, body });
      return Promise.resolve([]);
    },
  };
  const env = {
    DISCORD_CLIENT_ID: 'client-1',
  };

  await registerCommands({ rest, env, commands: [buscarCommand] });

  assert.equal(calls.length, 1);
  assert.equal(calls[0].route, Routes.applicationCommands('client-1'));
  assert.equal(calls[0].body.body[0].name, 'buscar');
});
