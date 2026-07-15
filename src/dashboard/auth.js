import { createHash, randomBytes as cryptoRandomBytes, timingSafeEqual } from 'node:crypto';

import cookieSession from 'cookie-session';

const SESSION_TTL_MS = 8 * 60 * 60 * 1_000;
const TOKEN_BYTES = 32;

function token(randomBytes) {
  return randomBytes(TOKEN_BYTES).toString('base64url');
}

function digest(value) {
  return createHash('sha256').update(String(value ?? ''), 'utf8').digest();
}

export function verifyDashboardCredentials(candidate, expected, compare = timingSafeEqual) {
  const usernameMatches = compare(digest(candidate?.username), digest(expected?.username));
  const passwordMatches = compare(digest(candidate?.password), digest(expected?.password));
  return Boolean(usernameMatches & passwordMatches);
}

export function createSessionRegistry({
  clock = Date.now,
  randomBytes = cryptoRandomBytes,
  maxEntries = 1_024,
  ttlMs = SESSION_TTL_MS,
} = {}) {
  const authenticated = new Map();
  const challenges = new Map();

  function cleanup() {
    const now = clock();
    for (const map of [authenticated, challenges]) {
      for (const [sid, entry] of map) {
        if (entry.expiresAt <= now) map.delete(sid);
      }
    }
    while (authenticated.size + challenges.size > maxEntries) {
      const firstChallenge = challenges.keys().next().value;
      if (firstChallenge !== undefined) challenges.delete(firstChallenge);
      else authenticated.delete(authenticated.keys().next().value);
    }
  }

  function createEntry(map, previousSid) {
    cleanup();
    if (previousSid) {
      authenticated.delete(previousSid);
      challenges.delete(previousSid);
    }
    const sid = token(randomBytes);
    const entry = Object.freeze({ expiresAt: clock() + ttlMs, csrfToken: token(randomBytes) });
    map.set(sid, entry);
    cleanup();
    return { sid, ...entry };
  }

  function get(map, sid) {
    cleanup();
    if (typeof sid !== 'string') return null;
    return map.get(sid) ?? null;
  }

  return {
    createAuthenticatedSession: (previousSid) => createEntry(authenticated, previousSid),
    createLoginChallenge: (previousSid) => createEntry(challenges, previousSid),
    getAuthenticated: (sid) => get(authenticated, sid),
    getLoginChallenge: (sid) => get(challenges, sid),
    revoke(sid) {
      authenticated.delete(sid);
      challenges.delete(sid);
    },
    debugSnapshot() {
      cleanup();
      return {
        authenticated: [...authenticated.entries()],
        challenges: [...challenges.entries()],
      };
    },
    get size() {
      cleanup();
      return authenticated.size + challenges.size;
    },
  };
}

function isSameOriginRequest(req) {
  const fetchSite = req.get?.('sec-fetch-site');
  if (fetchSite && !['same-origin', 'same-site', 'none'].includes(fetchSite)) return false;
  const origin = req.get?.('origin');
  if (!origin) return true;
  try {
    const expected = `${req.protocol ?? 'http'}://${req.get('host')}`;
    return new URL(origin).origin === expected;
  } catch {
    return false;
  }
}

function safeTokenEqual(candidate, expected) {
  if (typeof candidate !== 'string' || typeof expected !== 'string') return false;
  return timingSafeEqual(digest(candidate), digest(expected));
}

export function createDashboardAuth({
  env,
  logger = { info() {}, warn() {} },
  clock = Date.now,
  randomBytes = cryptoRandomBytes,
  registry = createSessionRegistry({ clock, randomBytes }),
} = {}) {
  const production = env?.NODE_ENV === 'production';
  const cookieConfiguration = {
    name: production ? '__Host-uade_dashboard' : 'uade_dashboard',
    keys: [env.DASHBOARD_SESSION_SECRET],
    httpOnly: true,
    sameSite: 'lax',
    secure: production,
    path: '/',
    maxAge: SESSION_TTL_MS,
  };
  const sessionMiddleware = cookieSession(cookieConfiguration);
  const cookieOptions = Object.freeze({ ...cookieConfiguration });

  function issueLoginChallenge(req) {
    const challenge = registry.createLoginChallenge(req.session?.sid);
    req.session = { sid: challenge.sid };
    return challenge.csrfToken;
  }

  function establishSession(req) {
    const session = registry.createAuthenticatedSession(req.session?.sid);
    req.session = { sid: session.sid };
    logger.info({ event: 'dashboard_login_succeeded' }, 'Dashboard login succeeded');
    return session;
  }

  function requireDashboardSession(req, res, next) {
    const entry = registry.getAuthenticated(req.session?.sid);
    if (!entry) {
      req.session = null;
      if (req.accepts?.('html') && !req.path?.startsWith('/api/')) return res.redirect(303, '/login');
      return res.status(401).json({ error: 'unauthorized' });
    }
    req.dashboardSession = entry;
    return next();
  }

  function requireMutationCsrf(req, res, next) {
    const sid = req.session?.sid;
    const entry = registry.getAuthenticated(sid) ?? registry.getLoginChallenge(sid);
    if (!entry || !isSameOriginRequest(req) || !safeTokenEqual(req.body?._csrf, entry.csrfToken)) {
      return res.status(403).send('Solicitud rechazada.');
    }
    return next();
  }

  function verifyCredentials(credentials) {
    return verifyDashboardCredentials(credentials, {
      username: env.DASHBOARD_USERNAME,
      password: env.DASHBOARD_PASSWORD,
    });
  }

  function revoke(req) {
    registry.revoke(req.session?.sid);
    req.session = null;
    logger.info({ event: 'dashboard_logout' }, 'Dashboard session revoked');
  }

  return {
    cookieOptions,
    sessionMiddleware,
    registry,
    issueLoginChallenge,
    establishSession,
    verifyCredentials,
    requireDashboardSession,
    requireMutationCsrf,
    revoke,
  };
}
