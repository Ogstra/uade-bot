import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import assert from 'node:assert/strict';

const readSource = (relativePath) => readFile(new URL(relativePath, import.meta.url), 'utf8');

test('CLI composition uses the HTTP engine behind an inert entrypoint guard', async () => {
  const source = await readSource('../cli.js');

  assert.match(source, /from ['"]\.\/automation\/http-session\.js['"]/);
  assert.match(source, /from ['"]\.\/automation\/http-search\.js['"]/);
  assert.match(source, /withHttpSession/);
  assert.match(source, /runHttpSearch/);
  assert.match(source, /import\.meta\.url\s*===\s*pathToFileURL\(process\.argv\[1\]\)\.href/);
  assert.doesNotMatch(source, /automation\/browser\.js|automation\/search\.js|withUadeContext|getBrowser|closeBrowser/);
});

test('bot composition injects the validated master key into pollOnce without browser pre-warm', async () => {
  const source = await readSource('../bot.js');

  assert.match(source, /from ['"]\.\/scheduler\/poller\.js['"]/);
  assert.match(source, /pollOnce/);
  assert.match(source, /masterKey:\s*env\.CREDENTIALS_MASTER_KEY/);
  assert.doesNotMatch(source, /automation\/browser\.js|getBrowser|browser_warm/);
});

test('routine entrypoint composition has no eager dotenv or Playwright import', async () => {
  const [cliSource, botSource, pollerSource] = await Promise.all([
    readSource('../cli.js'),
    readSource('../bot.js'),
    readSource('../scheduler/poller.js'),
  ]);

  for (const source of [cliSource, botSource, pollerSource]) {
    assert.doesNotMatch(source, /from ['"](?:dotenv|playwright)['"]|import\(['"](?:dotenv|playwright)['"]\)/);
  }
  assert.doesNotMatch(pollerSource, /automation\/browser\.js/);
});

test('browser boundary keeps plain-context cleanup explicit', async () => {
  const source = await readSource('./browser.js');

  assert.match(source, /export function createBrowserLifecycle/);
  assert.match(source, /import\(['"]playwright['"]\)/);
  assert.doesNotMatch(source, /^import\s+{\s*chromium\s*}\s+from\s+['"]playwright['"]/m);
  assert.match(source, /export async function withPlainContext/);
  assert.match(source, /finally\s*{[\s\S]*?context\.close\(\)/);
  assert.doesNotMatch(source, /withUadeContext/);
});
