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
      'dashboardPassword',
      '*.dashboardPassword',
      '*.*.dashboardPassword',
      '*.*.*.dashboardPassword',
      'DASHBOARD_PASSWORD',
      '*.DASHBOARD_PASSWORD',
      '*.*.DASHBOARD_PASSWORD',
      '*.*.*.DASHBOARD_PASSWORD',
      'sessionSecret',
      '*.sessionSecret',
      '*.*.sessionSecret',
      '*.*.*.sessionSecret',
      'DASHBOARD_SESSION_SECRET',
      '*.DASHBOARD_SESSION_SECRET',
      '*.*.DASHBOARD_SESSION_SECRET',
      '*.*.*.DASHBOARD_SESSION_SECRET',
      'cookie',
      '*.cookie',
      '*.*.cookie',
      '*.*.*.cookie',
      'sid',
      '*.sid',
      '*.*.sid',
      '*.*.*.sid',
      'csrfToken',
      '*.csrfToken',
      '*.*.csrfToken',
      '*.*.*.csrfToken',
      'CSRF_TOKEN',
      '*.CSRF_TOKEN',
      '*.*.CSRF_TOKEN',
      '*.*.*.CSRF_TOKEN',
      // Dashboard request bodies may contain both credentials and CSRF data.
      // Redact the complete body instead of trying to maintain a field allowlist.
      'body',
      '*.body',
      '*.*.body',
      '*.*.*.body',
    ],
    censor: '[REDACTED]',
  },
});

export default logger;
