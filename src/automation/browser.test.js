import { test, after } from 'node:test';
import assert from 'node:assert/strict';
import { closeBrowser, getBrowser, withPlainContext } from './browser.js';

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

test('withPlainContext() runs run(context), returns its result, and closes the context afterwards', async () => {
  let capturedContext;

  const result = await withPlainContext(async (context) => {
    capturedContext = context;
    return 'ok';
  });

  assert.equal(result, 'ok');
  // A closed BrowserContext rejects any further page operations -- same
  // pattern already used above to prove closeBrowser() actually closed
  // the browser.
  await assert.rejects(() => capturedContext.newPage());
});

test('withPlainContext() closes the context even when run(context) rejects', async () => {
  let capturedContext;

  await assert.rejects(
    withPlainContext(async (context) => {
      capturedContext = context;
      throw new Error('boom');
    }),
    /boom/,
  );

  await assert.rejects(() => capturedContext.newPage());
});
