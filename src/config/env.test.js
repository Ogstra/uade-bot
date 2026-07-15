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
  delete process.env.DISCORD_EPHEMERAL_REPLIES;
  delete process.env.DASHBOARD_ENABLED;
  delete process.env.DASHBOARD_PORT;
  delete process.env.DASHBOARD_USERNAME;
  delete process.env.DASHBOARD_PASSWORD;
  delete process.env.DASHBOARD_SESSION_SECRET;
});

afterEach(() => {
  resetEnv();
});

test('loadEnv() fails naming missing Discord variables without leaking values', () => {
  setBaseEnv();
  process.env.DISCORD_BOT_TOKEN = 'secret-discord-token';

  assert.throws(
    () => loadEnv({ loadDotenvFile: false }),
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

  const env = loadEnv({ loadDotenvFile: false });

  assert.equal(env.UADE_USERNAME, 'uade-user');
  assert.equal(env.UADE_PASSWORD, 'uade-pass');
  assert.equal(env.CREDENTIALS_MASTER_KEY, MASTER_KEY);
  assert.equal(env.DISCORD_BOT_TOKEN, 'discord-token');
  assert.equal(env.DISCORD_CLIENT_ID, 'discord-client-id');
  assert.equal(env.DISCORD_GUILD_ID, 'discord-guild-id');
  assert.deepEqual(env.DISCORD_GUILD_IDS, ['discord-guild-id']);
  assert.equal(env.POLL_INTERVAL_MS, 30000);
  assert.equal(env.SCHEDULER_CONCURRENCY, 2);
});

test('loadEnv() parses a comma-separated DISCORD_GUILD_ID into DISCORD_GUILD_IDS, trimming whitespace', () => {
  setBaseEnv();
  process.env.DISCORD_BOT_TOKEN = 'discord-token';
  process.env.DISCORD_CLIENT_ID = 'discord-client-id';
  process.env.DISCORD_GUILD_ID = 'guild-1, guild-2,guild-3';

  const env = loadEnv({ loadDotenvFile: false });

  assert.deepEqual(env.DISCORD_GUILD_IDS, ['guild-1', 'guild-2', 'guild-3']);
});

test('loadEnv() defaults DISCORD_EPHEMERAL_REPLIES to false (public replies) when unset', () => {
  setBaseEnv();
  process.env.DISCORD_BOT_TOKEN = 'discord-token';
  process.env.DISCORD_CLIENT_ID = 'discord-client-id';
  process.env.DISCORD_GUILD_ID = 'discord-guild-id';

  const env = loadEnv({ loadDotenvFile: false });

  assert.equal(env.DISCORD_EPHEMERAL_REPLIES, false);
});

test('loadEnv() coerces DISCORD_EPHEMERAL_REPLIES="true" to boolean true', () => {
  setBaseEnv();
  process.env.DISCORD_BOT_TOKEN = 'discord-token';
  process.env.DISCORD_CLIENT_ID = 'discord-client-id';
  process.env.DISCORD_GUILD_ID = 'discord-guild-id';
  process.env.DISCORD_EPHEMERAL_REPLIES = 'true';

  const env = loadEnv({ loadDotenvFile: false });

  assert.equal(env.DISCORD_EPHEMERAL_REPLIES, true);
});

test('loadEnv() rejects a non-"true"/"false" DISCORD_EPHEMERAL_REPLIES value', () => {
  setBaseEnv();
  process.env.DISCORD_BOT_TOKEN = 'discord-token';
  process.env.DISCORD_CLIENT_ID = 'discord-client-id';
  process.env.DISCORD_GUILD_ID = 'discord-guild-id';
  process.env.DISCORD_EPHEMERAL_REPLIES = 'yes';

  assert.throws(
    () => loadEnv({ loadDotenvFile: false }),
    (err) => {
      assert.match(err.message, /DISCORD_EPHEMERAL_REPLIES/);
      return true;
    },
  );
});

function setDiscordEnv() {
  process.env.DISCORD_BOT_TOKEN = 'discord-token';
  process.env.DISCORD_CLIENT_ID = 'discord-client-id';
  process.env.DISCORD_GUILD_ID = 'discord-guild-id';
}

test('loadEnv() keeps the dashboard disabled with safe typed defaults when DASHBOARD_* is unset', () => {
  setBaseEnv();
  setDiscordEnv();

  const env = loadEnv({ loadDotenvFile: false });

  assert.equal(env.DASHBOARD_ENABLED, false);
  assert.equal(env.DASHBOARD_PORT, 3000);
  assert.equal(env.DASHBOARD_USERNAME, 'admin');
  assert.equal(env.DASHBOARD_PASSWORD, 'admin');
  assert.equal(env.DASHBOARD_SESSION_SECRET, undefined);
});

test('loadEnv() coerces valid dashboard overrides to typed values', () => {
  setBaseEnv();
  setDiscordEnv();
  process.env.DASHBOARD_ENABLED = 'true';
  process.env.DASHBOARD_PORT = '4310';
  process.env.DASHBOARD_USERNAME = 'operator';
  process.env.DASHBOARD_PASSWORD = 'dashboard-password';
  process.env.DASHBOARD_SESSION_SECRET = 's'.repeat(32);

  const env = loadEnv({ loadDotenvFile: false });

  assert.equal(env.DASHBOARD_ENABLED, true);
  assert.equal(env.DASHBOARD_PORT, 4310);
  assert.equal(env.DASHBOARD_USERNAME, 'operator');
  assert.equal(env.DASHBOARD_PASSWORD, 'dashboard-password');
  assert.equal(env.DASHBOARD_SESSION_SECRET, 's'.repeat(32));
});

for (const [variable, rejectedValue] of [
  ['DASHBOARD_ENABLED', 'yes'],
  ['DASHBOARD_PORT', '0'],
  ['DASHBOARD_PORT', 'not-a-port'],
  ['DASHBOARD_USERNAME', ''],
  ['DASHBOARD_PASSWORD', ''],
  ['DASHBOARD_SESSION_SECRET', 'secret-too-short'],
]) {
  test(`loadEnv() rejects invalid ${variable} without echoing its value`, () => {
    setBaseEnv();
    setDiscordEnv();
    process.env[variable] = rejectedValue;

    assert.throws(
      () => loadEnv({ loadDotenvFile: false }),
      (err) => {
        assert.match(err.message, new RegExp(variable));
        if (rejectedValue) {
          assert.doesNotMatch(err.message, new RegExp(rejectedValue));
        }
        return true;
      },
    );
  });
}
