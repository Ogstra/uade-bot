import { rateLimit } from 'express-rate-limit';
import { z } from 'zod';

import { buildDashboardSnapshot } from './snapshot.js';
import { renderDashboardPage, renderLoginPage } from './render.js';

const GENERIC_LOGIN_ERROR = 'Usuario o contraseña incorrectos. Volvé a intentarlo.';
const LoginSchema = z.object({
  username: z.string().min(1).max(128),
  password: z.string().min(1).max(256),
  _csrf: z.string().min(1).max(256),
}).strict();

export function noStore(_req, res, next) {
  res.set('Cache-Control', 'no-store');
  res.set('Pragma', 'no-cache');
  next();
}

export function registerDashboardRoutes({
  app,
  auth,
  db,
  client,
  clock = Date.now,
  snapshotBuilder = buildDashboardSnapshot,
  renderLogin = renderLoginPage,
  renderDashboard = renderDashboardPage,
  loginLimit = 5,
}) {
  const limiter = rateLimit({
    windowMs: 15 * 60 * 1_000,
    limit: loginLimit,
    standardHeaders: 'draft-8',
    legacyHeaders: false,
    skipSuccessfulRequests: true,
    handler: (_req, res) => res.status(429).send('Demasiados intentos. Esperá unos minutos y volvé a intentar.'),
  });

  app.get('/login', noStore, (req, res) => {
    const csrfToken = auth.issueLoginChallenge(req);
    res.type('html').send(renderLogin({ csrfToken, error: null, cspNonce: res.locals.cspNonce }));
  });

  app.post('/login', noStore, limiter, auth.requireMutationCsrf, (req, res) => {
    const parsed = LoginSchema.safeParse(req.body);
    const valid = parsed.success && auth.verifyCredentials(parsed.data);
    if (!valid) {
      auth.recordLoginFailure();
      const csrfToken = auth.registry.getLoginChallenge(req.session?.sid)?.csrfToken ?? auth.issueLoginChallenge(req);
      return res.status(401).type('html').send(renderLogin({ csrfToken, error: GENERIC_LOGIN_ERROR, cspNonce: res.locals.cspNonce }));
    }
    auth.establishSession(req);
    return res.redirect(303, '/dashboard');
  });

  app.post('/logout', noStore, auth.requireDashboardSession, auth.requireMutationCsrf, (req, res) => {
    auth.revoke(req);
    res.redirect(303, '/login');
  });

  app.get('/dashboard', noStore, auth.requireDashboardSession, (req, res) => {
    const snapshot = snapshotBuilder({ db, client, now: clock });
    res.type('html').send(renderDashboard({ snapshot, csrfToken: req.dashboardSession.csrfToken, cspNonce: res.locals.cspNonce }));
  });

  app.get('/api/dashboard', noStore, auth.requireDashboardSession, (req, res) => {
    res.json(snapshotBuilder({ db, client, now: clock }));
  });
}
