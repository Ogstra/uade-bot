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
    // --disable-gpu: headless never needs GPU compositing. --no-sandbox:
    // standard for containerized Chromium (CLAUDE.md) -- the container
    // itself provides isolation. --disable-dev-shm-usage: avoids /dev/shm
    // size limits on small VPS instances by using disk-backed temp files
    // instead. All three trim Chromium's baseline memory footprint.
    launchPromise = chromium.launch({
      headless: true,
      args: ['--disable-gpu', '--no-sandbox', '--disable-dev-shm-usage'],
    });
  }

  sharedBrowser = await launchPromise;
  logger.info({ event: 'browser_launch_done' }, 'Shared Chromium browser ready');
  return sharedBrowser;
}

/**
 * Closes the shared Chromium `Browser` singleton if one is open, and clears
 * the singleton so the next `getBrowser()` call relaunches it lazily.
 * No-op (and never throws) if no browser is currently open — safe to call
 * on every idle scheduler tick (queue.js) without guarding first.
 *
 * @returns {Promise<void>}
 */
export async function closeBrowser() {
  if (!sharedBrowser) {
    return;
  }

  const browser = sharedBrowser;
  sharedBrowser = undefined;
  launchPromise = undefined;

  try {
    await browser.close();
    logger.info({ event: 'browser_closed' }, 'Shared Chromium browser closed (idle)');
  } catch (err) {
    logger.error({ event: 'browser_close_failed', message: err.message }, 'Failed to close idle Chromium browser');
  }
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

/**
 * Opens a fresh `BrowserContext` with NO `httpCredentials` -- for the SSO
 * relink flow (Fase 3.1), which authenticates via a Microsoft/Azure AD login
 * form, not Basic Auth. Invokes `run(context)` and always closes the context
 * in a `finally` block, same lifecycle contract as `withUadeContext`, and
 * reuses the same `getBrowser()` singleton -- never launches a second
 * `Browser` process.
 *
 * @template T
 * @param {(context: import('playwright').BrowserContext) => Promise<T>} run
 * @returns {Promise<T>}
 */
export async function withPlainContext(run) {
  const browser = await getBrowser();
  const context = await browser.newContext();

  try {
    return await run(context);
  } finally {
    await context.close();
  }
}
