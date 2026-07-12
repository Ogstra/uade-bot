import { test, after } from 'node:test';
import assert from 'node:assert/strict';
import { closeBrowser, getBrowser } from './browser.js';

after(async () => {
  await closeBrowser();
});

test('closeBrowser() closes the shared browser and getBrowser() relaunches it lazily afterwards', async () => {
  const first = await getBrowser();
  assert.equal(first.isConnected(), true);

  await closeBrowser();
  assert.equal(first.isConnected(), false);

  const second = await getBrowser();
  assert.equal(second.isConnected(), true);
  assert.notEqual(second, first);
});

test('closeBrowser() is a no-op when no browser is currently open', async () => {
  await closeBrowser();
  await assert.doesNotReject(() => closeBrowser());
});
