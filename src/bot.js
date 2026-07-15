import { pathToFileURL } from 'node:url';

import { loadEnv } from './config/env.js';
import { getDb } from './db/database.js';
import { listJobsByUser } from './db/jobs.repository.js';
import { createDiscordClient } from './discord/client.js';
import { commandsByName } from './discord/commands/index.js';
import { createInteractionHandler } from './discord/interactions.js';
import { createNotificationDispatcher } from './discord/notifications.js';
import { createScheduler } from './scheduler/queue.js';
import { reconstructActiveJobs } from './scheduler/bootstrap.js';
import { getBrowser } from './automation/browser.js';
import { startDashboardServer } from './dashboard/server.js';
import logger from './logger.js';

let dashboardServer = null;

export function getDashboardServer() {
  return dashboardServer;
}

export function startOptionalDashboard({
  db,
  client,
  env,
  logger: runtimeLogger = logger,
  startServer = startDashboardServer,
}) {
  if (!env.DASHBOARD_ENABLED) return null;
  return Promise.resolve(startServer({ db, client, env, logger: runtimeLogger }))
    .then((server) => {
      dashboardServer = server;
      runtimeLogger.info(
        { event: 'dashboard_server_started', port: env.DASHBOARD_PORT },
        'Dashboard server started',
      );
      return server;
    })
    .catch((error) => {
      runtimeLogger.error(
        { event: 'dashboard_server_failed', message: error.message },
        'Dashboard server failed to start',
      );
      return null;
    });
}

export async function main() {
  const env = loadEnv();
  const db = getDb();

  // Launch the shared Chromium browser right away, in parallel with the
  // Discord client/login below, so it's already warm (or nearly so) by the
  // time the first search runs instead of paying cold-start latency on
  // that first poll. Not awaited -- must never delay bot startup. It
  // closes itself on the next idle scheduler tick (queue.js) if nothing
  // ends up polling it.
  getBrowser().catch((err) => {
    logger.error({ event: 'browser_warm_failed', message: err.message }, 'Failed to pre-warm the shared browser at startup');
  });

  const client = createDiscordClient();
  const notifications = createNotificationDispatcher({ client, db });
  const scheduler = createScheduler({ db, onJobPolled: notifications.onJobPolled });
  // The HTTP listener shares this process's exact DB and Discord cache. Do not
  // await it: dashboard startup must not delay gateway login or scheduler ready.
  startOptionalDashboard({ db, client, env, logger });

  client.on(
    'interactionCreate',
    createInteractionHandler({
      commandsByName,
      env,
      logger,
      commandContext: {
        onJobCreated: scheduler.pollJobNow,
        onJobResumed: scheduler.pollJobNow,
        // After /credenciales saves/rotates credentials (Fase 3.1), poll
        // every one of that account's ACTIVE jobs immediately instead of
        // leaving them to wait up to POLL_INTERVAL_MS for the next
        // scheduled tick -- same immediacy onJobResumed already gives a
        // single job via /reanudar. Paused jobs are left alone; credential
        // changes don't implicitly resume them.
        onCredentialsUpdated: (discordUserId) => {
          const activeJobs = listJobsByUser(db, discordUserId).filter((job) => job.status === 'active');
          activeJobs.forEach((job) => scheduler.pollJobNow(job));
        },
        ephemeralReplies: env.DISCORD_EPHEMERAL_REPLIES,
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

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  main().catch((err) => {
    logger.error({ event: 'discord_bot_failed', message: err.message }, 'Discord bot failed to start');
    process.exitCode = 1;
  });
}
