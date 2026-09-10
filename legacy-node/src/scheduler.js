import { loadEnv } from './config/env.js';
import { getDb } from './db/database.js';
import { createScheduler } from './scheduler/queue.js';
import { reconstructActiveJobs } from './scheduler/bootstrap.js';
import { pollOnce } from './scheduler/poller.js';
import logger from './logger.js';

/**
 * The phase's runnable, long-lived scheduler process entry point — this is
 * the literal subject of the Roadmap's Success Criterion #5 ("restarting
 * the process never duplicates polling work"). Unlike `src/cli.js`'s
 * one-shot run, this process never calls `process.exit()` — the running
 * `setInterval` started by `scheduler.start()` is what keeps the process
 * alive by design. Every diagnostic is routed through the shared,
 * redacting `logger` — never a direct console call (T-02-12).
 */
async function main() {
  const env = loadEnv();
  const db = getDb();
  const scheduler = createScheduler({
    db,
    pollOnceFn: (database, jobId) => pollOnce(database, jobId, { masterKey: env.CREDENTIALS_MASTER_KEY }),
  });

  await reconstructActiveJobs(db, scheduler);

  scheduler.start({ immediate: true });

  logger.info({ event: 'scheduler_started' }, 'Scheduler started');
}

main()
  .then(() => {})
  .catch((err) => {
    logger.error({ event: 'scheduler_failed', message: err.message }, 'Scheduler failed to start');
    process.exitCode = 1;
  });
