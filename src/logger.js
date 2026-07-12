import pino from 'pino';

/**
 * The single logging entry point for the whole engine. Every other module
 * imports `logger` from here rather than writing to the console directly,
 * so credential redaction is enforced in exactly one place.
 *
 * Redacts password/credential/authorization-shaped fields at any nesting
 * depth so a future logged object (e.g. an accidental full options dump)
 * can't leak a UADE password or Basic Auth header.
 */
const logger = pino({
  redact: {
    paths: [
      'password',
      '*.password',
      '*.*.password',
      'token',
      '*.token',
      '*.*.token',
      'discordToken',
      '*.discordToken',
      '*.*.discordToken',
      'DISCORD_BOT_TOKEN',
      '*.DISCORD_BOT_TOKEN',
      '*.*.DISCORD_BOT_TOKEN',
      'httpCredentials',
      '*.httpCredentials',
      'httpCredentials.password',
      '*.httpCredentials.password',
      'authorization',
      '*.authorization',
      '*.*.authorization',
      'headers.authorization',
      '*.headers.authorization',
      'masterKey',
      '*.masterKey',
      'derivedKey',
      '*.derivedKey',
      'uadeUsername',
      '*.uadeUsername',
      '*.*.uadeUsername',
      'uadePassword',
      '*.uadePassword',
      '*.*.uadePassword',
      'uadeStartUrl',
      '*.uadeStartUrl',
      '*.*.uadeStartUrl',
    ],
    censor: '[REDACTED]',
  },
});

export default logger;
