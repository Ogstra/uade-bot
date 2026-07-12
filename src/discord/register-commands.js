import { REST, Routes } from 'discord.js';
import { pathToFileURL } from 'node:url';
import { loadEnv } from '../config/env.js';
import logger from '../logger.js';
import { commands as defaultCommands } from './commands/index.js';

export async function registerCommands({ rest, env, commands = defaultCommands } = {}) {
  const body = commands.map((command) => command.data.toJSON());
  const guildIds = env.DISCORD_GUILD_IDS ?? [env.DISCORD_GUILD_ID];

  // Discord has no bulk multi-guild registration endpoint -- one PUT per
  // authorized guild id, each guild-scoped (never global commands).
  for (const guildId of guildIds) {
    const route = Routes.applicationGuildCommands(env.DISCORD_CLIENT_ID, guildId);
    await rest.put(route, { body });
    logger.info(
      { event: 'discord_commands_registered', guildId, count: body.length },
      'Discord commands registered',
    );
  }
}

async function main() {
  const env = loadEnv();
  const rest = new REST({ version: '10' }).setToken(env.DISCORD_BOT_TOKEN);

  await registerCommands({ rest, env });
}

if (import.meta.url === pathToFileURL(process.argv[1]).href) {
  main().catch((err) => {
    logger.error(
      { event: 'discord_command_registration_failed', message: err.message },
      'Discord command registration failed',
    );
    process.exitCode = 1;
  });
}
