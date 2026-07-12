import { loadEnv } from './config/env.js';
import { getDb } from './db/database.js';
import { createDiscordClient } from './discord/client.js';
import { commandsByName } from './discord/commands/index.js';
import { createInteractionHandler } from './discord/interactions.js';
import { createNotificationDispatcher } from './discord/notifications.js';
import { createScheduler } from './scheduler/queue.js';
import { reconstructActiveJobs } from './scheduler/bootstrap.js';
import logger from './logger.js';

async function main() {
  const env = loadEnv();
  const db = getDb();
  const client = createDiscordClient();
  const notifications = createNotificationDispatcher({ client, db });
  const scheduler = createScheduler({ db, onJobPolled: notifications.onJobPolled });

  client.on(
    'interactionCreate',
    createInteractionHandler({
      commandsByName,
      env,
      logger,
      commandContext: {
        onJobCreated: scheduler.pollJobNow,
      },
    }),
  );
  client.once('ready', async () => {
    await reconstructActiveJobs(db, scheduler);
    scheduler.start({ immediate: true });
    logger.info({ event: 'discord_bot_ready', userId: client.user?.id }, 'Discord bot ready');
  });

  await client.login(env.DISCORD_BOT_TOKEN);
}

main().catch((err) => {
  logger.error({ event: 'discord_bot_failed', message: err.message }, 'Discord bot failed to start');
  process.exitCode = 1;
});
