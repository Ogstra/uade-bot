import logger from '../logger.js';

const LAUNCH_OPTIONS = Object.freeze({
  headless: true,
  args: ['--disable-gpu', '--no-sandbox', '--disable-dev-shm-usage'],
});

async function launchChromium() {
  const { chromium } = await import('playwright');
  return chromium.launch(LAUNCH_OPTIONS);
}

/**
 * Creates the lazy browser lifecycle used only by SSO onboarding/relink.
 * Injecting the launcher keeps lifecycle tests browserless and guarantees
 * importing this module never resolves Playwright by itself.
 *
 * @param {{
 *   launchBrowser?: () => Promise<import('playwright').Browser>,
 *   logger?: typeof logger,
 * }} deps
 */
export function createBrowserLifecycle({
  launchBrowser = launchChromium,
  logger: runtimeLogger = logger,
} = {}) {
  /** @type {import('playwright').Browser | undefined} */
  let sharedBrowser;
  /** @type {Promise<import('playwright').Browser> | undefined} */
  let launchPromise;

  async function getBrowser() {
    if (sharedBrowser) {
      return sharedBrowser;
    }

    if (!launchPromise) {
      runtimeLogger.info({ event: 'browser_launch_start' }, 'Launching shared Chromium browser');
      launchPromise = Promise.resolve().then(launchBrowser);
    }

    try {
      sharedBrowser = await launchPromise;
    } catch (error) {
      launchPromise = undefined;
      throw error;
    }
    runtimeLogger.info({ event: 'browser_launch_done' }, 'Shared Chromium browser ready');
    return sharedBrowser;
  }

  async function closeBrowser() {
    if (!sharedBrowser) {
      return;
    }

    const browser = sharedBrowser;
    sharedBrowser = undefined;
    launchPromise = undefined;

    try {
      await browser.close();
      runtimeLogger.info({ event: 'browser_closed' }, 'Shared Chromium browser closed (idle)');
    } catch (error) {
      runtimeLogger.error(
        { event: 'browser_close_failed', message: error.message },
        'Failed to close idle Chromium browser',
      );
    }
  }

  async function withPlainContext(run) {
    const browser = await getBrowser();
    const context = await browser.newContext();

    try {
      return await run(context);
    } finally {
      await context.close();
    }
  }

  return { getBrowser, closeBrowser, withPlainContext };
}

const sharedLifecycle = createBrowserLifecycle();

/** @returns {Promise<import('playwright').Browser>} */
export async function getBrowser() {
  return sharedLifecycle.getBrowser();
}

/** @returns {Promise<void>} */
export async function closeBrowser() {
  return sharedLifecycle.closeBrowser();
}

/**
 * Opens a fresh context without Basic Auth for the exceptional Microsoft
 * SSO onboarding/relink flow and always closes it before returning.
 *
 * @template T
 * @param {(context: import('playwright').BrowserContext) => Promise<T>} run
 * @returns {Promise<T>}
 */
export async function withPlainContext(run) {
  return sharedLifecycle.withPlainContext(run);
}
