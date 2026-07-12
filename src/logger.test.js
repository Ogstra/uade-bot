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
