import { CookieJar } from 'tough-cookie';

const PRODUCTION_ORIGIN = 'https://inscripcionespia.uade.edu.ar';
const DEFAULT_MAX_BODY_BYTES = 109_175;
const DEFAULT_TIMEOUT_MS = 10_000;
const DEFAULT_MAX_REDIRECTS = 5;
const REDIRECT_STATUSES = new Set([301, 302, 303, 307, 308]);

function httpError(code) {
  const error = new Error(code);
  error.code = code;
  return error;
}

function positiveInteger(value, code, { allowZero = false } = {}) {
  const valid = Number.isSafeInteger(value) && (allowZero ? value >= 0 : value > 0);
  if (!valid) {
    throw httpError(code);
  }
  return value;
}

function parseUrl(value, code = 'HTTP_INVALID_URL') {
  try {
    return new URL(value);
  } catch {
    throw httpError(code);
  }
}

function normalizeAllowedOrigin(value) {
  const parsed = parseUrl(value, 'HTTP_INVALID_ALLOWED_ORIGIN');
  if (parsed.username || parsed.password || parsed.pathname !== '/' || parsed.search || parsed.hash) {
    throw httpError('HTTP_INVALID_ALLOWED_ORIGIN');
  }
  return parsed.origin;
}

function setHeader(headers, name, value) {
  if (value === undefined || value === null) {
    return;
  }
  headers.set(name, String(value));
}

async function storeResponseCookies(jar, response, responseUrl) {
  const values = typeof response.headers.getSetCookie === 'function'
    ? response.headers.getSetCookie()
    : response.headers.get('set-cookie')
      ? [response.headers.get('set-cookie')]
      : [];

  try {
    for (const value of values) {
      await jar.setCookie(value, responseUrl);
    }
  } catch {
    throw httpError('HTTP_COOKIE_REJECTED');
  }
}

async function readBoundedBody(response, maxBodyBytes) {
  if (!response.body) {
    return '';
  }

  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let total = 0;
  let body = '';

  try {
    while (true) {
      const { done, value } = await reader.read();
      if (done) {
        break;
      }
      total += value.byteLength;
      if (total > maxBodyBytes) {
        await reader.cancel().catch(() => {});
        throw httpError('HTTP_BODY_TOO_LARGE');
      }
      body += decoder.decode(value, { stream: true });
    }
    body += decoder.decode();
    return body;
  } finally {
    reader.releaseLock();
  }
}

function redirectedRequest(status, method, body) {
  if (status === 303 || ((status === 301 || status === 302) && method === 'POST')) {
    return { method: 'GET', body: undefined };
  }
  return { method, body };
}

/**
 * Creates one ephemeral HTTP session, scoped to exactly one allowed origin.
 * The CookieJar and Basic credentials are reachable only during `run` and no
 * native transport message is exposed to callers.
 *
 * @template T
 * @param {{ username: string, password: string }} credentials
 * @param {(session: { request: (url: string | URL, options?: object) => Promise<object> }) => Promise<T>} run
 * @param {{ allowedOrigin?: string, maxBodyBytes?: number, timeoutMs?: number, maxRedirects?: number, fetchImpl?: typeof fetch }} [options]
 * @returns {Promise<T>}
 */
export async function withHttpSession({ username, password }, run, options = {}) {
  const allowedOrigin = normalizeAllowedOrigin(options.allowedOrigin ?? PRODUCTION_ORIGIN);
  const sessionMaxBodyBytes = positiveInteger(options.maxBodyBytes ?? DEFAULT_MAX_BODY_BYTES, 'HTTP_INVALID_BODY_LIMIT');
  const sessionTimeoutMs = positiveInteger(options.timeoutMs ?? DEFAULT_TIMEOUT_MS, 'HTTP_INVALID_TIMEOUT');
  const maxRedirects = positiveInteger(
    options.maxRedirects ?? DEFAULT_MAX_REDIRECTS,
    'HTTP_INVALID_REDIRECT_LIMIT',
    { allowZero: true },
  );
  const fetchImpl = options.fetchImpl ?? fetch;
  if (typeof username !== 'string' || typeof password !== 'string' || typeof run !== 'function' || typeof fetchImpl !== 'function') {
    throw httpError('HTTP_INVALID_SESSION_OPTIONS');
  }

  const jar = new CookieJar();

  const request = async (url, requestOptions = {}) => {
    let currentUrl = parseUrl(url);
    if (currentUrl.origin !== allowedOrigin) {
      throw httpError('HTTP_ORIGIN_NOT_ALLOWED');
    }

    let method = String(requestOptions.method ?? 'GET').toUpperCase();
    let body = requestOptions.body;
    const timeoutMs = positiveInteger(requestOptions.timeoutMs ?? sessionTimeoutMs, 'HTTP_INVALID_TIMEOUT');
    const maxBodyBytes = positiveInteger(requestOptions.maxBodyBytes ?? sessionMaxBodyBytes, 'HTTP_INVALID_BODY_LIMIT');
    if (maxBodyBytes > sessionMaxBodyBytes) {
      throw httpError('HTTP_INVALID_BODY_LIMIT');
    }

    const baseHeaders = new Headers(requestOptions.headers);
    baseHeaders.delete('authorization');
    baseHeaders.delete('cookie');
    baseHeaders.delete('host');
    const visited = new Set();
    let redirectCount = 0;

    while (true) {
      if (currentUrl.origin !== allowedOrigin) {
        throw httpError('HTTP_ORIGIN_NOT_ALLOWED');
      }
      const currentKey = currentUrl.href;
      if (visited.has(currentKey)) {
        throw httpError('HTTP_REDIRECT_LOOP');
      }
      visited.add(currentKey);

      const headers = new Headers(baseHeaders);
      setHeader(headers, 'authorization', `Basic ${Buffer.from(`${username}:${password}`).toString('base64')}`);
      const cookie = await jar.getCookieString(currentUrl);
      setHeader(headers, 'cookie', cookie || undefined);
      if (body === undefined) {
        headers.delete('content-length');
        headers.delete('content-type');
      }

      const controller = new AbortController();
      const timer = setTimeout(() => controller.abort(), timeoutMs);
      let response;
      try {
        response = await fetchImpl(currentUrl, {
          method,
          headers,
          body,
          redirect: 'manual',
          signal: controller.signal,
        });
        await storeResponseCookies(jar, response, currentUrl);

        if (REDIRECT_STATUSES.has(response.status)) {
          if (redirectCount >= maxRedirects) {
            throw httpError('HTTP_TOO_MANY_REDIRECTS');
          }
          const location = response.headers.get('location');
          if (!location) {
            throw httpError('HTTP_REDIRECT_LOCATION_MISSING');
          }
          const nextUrl = parseUrl(new URL(location, currentUrl), 'HTTP_REDIRECT_INVALID');
          if (currentUrl.protocol === 'https:' && nextUrl.protocol !== 'https:') {
            throw httpError('HTTP_REDIRECT_DOWNGRADE');
          }
          if (nextUrl.origin !== allowedOrigin) {
            throw httpError('HTTP_REDIRECT_CROSS_ORIGIN');
          }
          await response.body?.cancel().catch(() => {});
          ({ method, body } = redirectedRequest(response.status, method, body));
          currentUrl = nextUrl;
          redirectCount += 1;
          continue;
        }

        const responseBody = await readBoundedBody(response, maxBodyBytes);
        return {
          status: response.status,
          body: responseBody,
          url: currentUrl.href,
          contentType: response.headers.get('content-type') ?? '',
        };
      } catch (error) {
        if (typeof error?.code === 'string' && error.code.startsWith('HTTP_')) {
          throw error;
        }
        if (controller.signal.aborted || error?.name === 'AbortError' || error?.name === 'TimeoutError') {
          throw httpError('HTTP_TIMEOUT');
        }
        throw httpError('HTTP_TRANSPORT_ERROR');
      } finally {
        clearTimeout(timer);
      }
    }
  };

  try {
    return await run({ request });
  } finally {
    // Drop the only references to the request closure and its jar when this
    // scope ends. tough-cookie keeps no global store for a default jar.
  }
}

export const HTTP_SESSION_LIMITS = Object.freeze({
  maxBodyBytes: DEFAULT_MAX_BODY_BYTES,
  timeoutMs: DEFAULT_TIMEOUT_MS,
  maxRedirects: DEFAULT_MAX_REDIRECTS,
});
