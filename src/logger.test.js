import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { test } from 'node:test';

test('logger redacts Discord and UADE credential fields in nested objects', () => {
  const script = `
    import logger from './src/logger.js';
    logger.info({
      discordToken: 'discord-token-secret',
      token: 'generic-token-secret',
      DISCORD_BOT_TOKEN: 'env-token-secret',
      nested: {
        uadeUsername: 'uade-user-secret',
        uadePassword: 'uade-password-secret',
        uadeStartUrl: 'https://inscripcionespia.uade.edu.ar/secret',
      },
    }, 'redaction test');
  `;

  const result = spawnSync(process.execPath, ['--input-type=module', '-e', script], {
    cwd: process.cwd(),
    encoding: 'utf8',
  });

  assert.equal(result.status, 0, result.stderr);
  assert.doesNotMatch(result.stdout, /discord-token-secret/);
  assert.doesNotMatch(result.stdout, /generic-token-secret/);
  assert.doesNotMatch(result.stdout, /env-token-secret/);
  assert.doesNotMatch(result.stdout, /uade-user-secret/);
  assert.doesNotMatch(result.stdout, /uade-password-secret/);
  assert.doesNotMatch(result.stdout, /inscripcionespia\.uade\.edu\.ar\/secret/);
  assert.match(result.stdout, /\[REDACTED\]/);
});

test('logger redacts dashboard credentials, sessions, cookies, CSRF tokens, and request bodies', () => {
  const sentinels = [
    'dashboard-password-sentinel',
    'dashboard-env-password-sentinel',
    'session-secret-sentinel',
    'dashboard-env-session-sentinel',
    'cookie-sentinel',
    'sid-sentinel',
    'csrf-sentinel',
    'request-body-sentinel',
  ];
  const script = `
    import logger from './src/logger.js';
    logger.info({
      dashboardPassword: '${sentinels[0]}',
      config: { DASHBOARD_PASSWORD: '${sentinels[1]}' },
      auth: { sessionSecret: '${sentinels[2]}' },
      nested: { config: { DASHBOARD_SESSION_SECRET: '${sentinels[3]}' } },
      request: {
        headers: { cookie: '${sentinels[4]}' },
        session: { sid: '${sentinels[5]}', csrfToken: '${sentinels[6]}' },
        body: { password: '${sentinels[7]}' },
      },
    }, 'dashboard redaction test');
  `;

  const result = spawnSync(process.execPath, ['--input-type=module', '-e', script], {
    cwd: process.cwd(),
    encoding: 'utf8',
  });

  assert.equal(result.status, 0, result.stderr);
  for (const sentinel of sentinels) {
    assert.doesNotMatch(result.stdout, new RegExp(sentinel));
  }
  assert.match(result.stdout, /\[REDACTED\]/);
});
