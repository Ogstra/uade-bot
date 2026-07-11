import { chromium } from 'playwright';
import logger from '../logger.js';

/** @type {import('playwright').Browser | undefined} */
let sharedBrowser;

/** @type {Promise<import('playwright').Browser> | undefined} */
let launchPromise;

/**
 * Returns the shared Chromium `Browser` singleton, launching it lazily on
 * first call. Every subsequent call reuses the same instance — a second
 * `Browser` process is never launched (see ARCHITECTURE.md's "shared
 * Browser, context-per-run" pattern).
 *
 * @returns {Promise<import('playwright').Browser>}
 */
export async function getBrowser() {
  if (sharedBrowser) {
    return sharedBrowser;
  }

  if (!launchPromise) {
    logger.info({ event: 'browser_launch_start' }, 'Launching shared Chromium browser');
    launchPromise = chromium.launch({ headless: true });
  }

  sharedBrowser = await launchPromise;
  logger.info({ event: 'browser_launch_done' }, 'Shared Chromium browser ready');
  return sharedBrowser;
}

/**
 * Opens a fresh `BrowserContext` carrying the given `httpCredentials`,
 * invokes `run(context)`, and always closes the context in a `finally`
 * block so the context — and the credentials it carries — never outlives
 * a single call.
 *
 * `username`/`password` are never assigned to any module-level variable;
 * they exist only for the duration of this function call and are handed
 * directly to Playwright's `newContext` option.
 *
 * @template T
 * @param {{ username: string, password: string }} credentials
 * @param {(context: import('playwright').BrowserContext) => Promise<T>} run
 * @returns {Promise<T>}
 */
export async function withUadeContext({ username, password }, run) {
  const browser = await getBrowser();
  const context = await browser.newContext({
    httpCredentials: { username, password },
  });

  try {
    return await run(context);
  } finally {
    await context.close();
  }
}
