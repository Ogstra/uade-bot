import { randomBytes as cryptoRandomBytes } from 'node:crypto';

import express from 'express';
import helmet from 'helmet';

import { createDashboardAuth } from './auth.js';
import { renderSafeError } from './render.js';
import { registerDashboardRoutes } from './routes.js';

export function createDashboardApp({
  db,
  client,
  env,
  logger = { info() {}, warn() {}, error() {} },
  clock = Date.now,
  randomBytes = cryptoRandomBytes,
  snapshotBuilder,
  renderLogin,
  renderDashboard,
  loginLimit,
}) {
  const resolvedEnv = { ...env };
  if (!resolvedEnv.DASHBOARD_SESSION_SECRET) {
    resolvedEnv.DASHBOARD_SESSION_SECRET = randomBytes(32).toString('base64url');
    logger.warn(
      { event: 'dashboard_ephemeral_session_secret', ephemeral: true },
      'Dashboard sessions will reset when the process restarts',
    );
  }
  if (resolvedEnv.DASHBOARD_USERNAME === 'admin' && resolvedEnv.DASHBOARD_PASSWORD === 'admin') {
    logger.warn(
      { event: 'dashboard_default_credentials', defaultsActive: true },
      'Dashboard development credentials are still active',
    );
  }

  const app = express();
  app.disable('x-powered-by');
  app.set('trust proxy', false);
  app.use((_req, res, next) => {
    res.locals.cspNonce = randomBytes(16).toString('base64url');
    next();
  });
  app.use(helmet({
    contentSecurityPolicy: {
      directives: {
        defaultSrc: ["'self'"],
        scriptSrc: ["'self'", (_req, res) => `'nonce-${res.locals.cspNonce}'`],
        styleSrc: ["'self'", (_req, res) => `'nonce-${res.locals.cspNonce}'`],
        objectSrc: ["'none'"],
        baseUri: ["'self'"],
        frameAncestors: ["'none'"],
        upgradeInsecureRequests: null,
      },
    },
    referrerPolicy: { policy: 'same-origin' },
    strictTransportSecurity: false,
  }));
  app.use(express.urlencoded({ extended: false, limit: '4kb', parameterLimit: 10 }));

  const auth = createDashboardAuth({ env: resolvedEnv, logger, clock, randomBytes });
  app.use(auth.sessionMiddleware);
  registerDashboardRoutes({
    app,
    auth,
    db,
    client,
    clock,
    snapshotBuilder,
    renderLogin,
    renderDashboard,
    loginLimit,
  });
  app.use((_error, req, res, _next) => {
    logger.error({ event: 'dashboard_request_failed' }, 'Dashboard request failed');
    if (res.headersSent) return;
    if (req.accepts('html') && !req.path.startsWith('/api/')) {
      return res.status(500).type('html').send(renderSafeError({ cspNonce: res.locals.cspNonce, status: 500 }));
    }
    return res.status(500).json({ error: 'internal_error' });
  });
  return app;
}

export async function startDashboardServer(deps) {
  const app = createDashboardApp(deps);
  const port = deps.env.DASHBOARD_PORT;
  return new Promise((resolve, reject) => {
    const server = app.listen(port, '0.0.0.0', () => resolve(server));
    server.once('error', reject);
  });
}
