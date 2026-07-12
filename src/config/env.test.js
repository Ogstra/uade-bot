import assert from 'node:assert/strict';
import { afterEach, beforeEach, test } from 'node:test';
import { loadEnv } from './env.js';

const ORIGINAL_ENV = { ...process.env };
const MASTER_KEY = 'a'.repeat(64);

function resetEnv() {
  process.env = { ...ORIGINAL_ENV };
}

function setBaseEnv() {
  process.env.UADE_USERNAME = 'uade-user';
  process.env.UADE_PASSWORD = 'uade-pass';
  process.env.CREDENTIALS_MASTER_KEY = MASTER_KEY;
}

beforeEach(() => {
  resetEnv();
  delete process.env.DISCORD_BOT_TOKEN;
  delete process.env.DISCORD_CLIENT_ID;
  delete process.env.DISCORD_GUILD_ID;
});

afterEach(() => {
  resetEnv();
});

test('loadEnv() fails naming missing Discord variables without leaking values', () => {
  setBaseEnv();
  process.env.DISCORD_BOT_TOKEN = 'secret-discord-token';

  assert.throws(
    () => loadEnv(),
    (err) => {
      assert.match(err.message, /DISCORD_CLIENT_ID/);
      assert.match(err.message, /DISCORD_GUILD_ID/);
      assert.doesNotMatch(err.message, /secret-discord-token/);
      return true;
    },
  );
});

test('loadEnv() returns Discord variables alongside prior Phase 1 and 2 variables', () => {
  setBaseEnv();
  process.env.DISCORD_BOT_TOKEN = 'discord-token';
  process.env.DISCORD_CLIENT_ID = 'discord-client-id';
  process.env.DISCORD_GUILD_ID = 'discord-guild-id';
  process.env.POLL_INTERVAL_MS = '30000';
  process.env.SCHEDULER_CONCURRENCY = '2';

  const env = loadEnv();

  assert.equal(env.UADE_USERNAME, 'uade-user');
  assert.equal(env.UADE_PASSWORD, 'uade-pass');
  assert.equal(env.CREDENTIALS_MASTER_KEY, MASTER_KEY);
  assert.equal(env.DISCORD_BOT_TOKEN, 'discord-token');
  assert.equal(env.DISCORD_CLIENT_ID, 'discord-client-id');
  assert.equal(env.DISCORD_GUILD_ID, 'discord-guild-id');
  assert.equal(env.POLL_INTERVAL_MS, 30000);
  assert.equal(env.SCHEDULER_CONCURRENCY, 2);
});
