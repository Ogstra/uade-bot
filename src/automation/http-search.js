import { load } from 'cheerio/slim';

import { FiltrosSchema } from '../schemas.js';
import { parseDeltaResponse } from './delta-response.js';
import {
  buildSearchPayload,
  extractReflectedSearchState,
  verifyPostbackMatchesQuery,
} from './webforms.js';

const DELTA_FAILURE_REASONS = Object.freeze({
  delta_error: 'delta_error',
  delta_redirect: 'delta_redirect',
  delta_too_large: 'delta_too_large',
  delta_too_many_nodes: 'delta_too_many_nodes',
});

function searchFailed(reason) {
  return { status: 'search_failed', reason };
}

function statusOutcome(status) {
  if (status === 401) {
    return { status: 'invalid_credentials' };
  }
  if (status === 429) {
    return { status: 'rate_limited' };
  }
  return null;
}

function transportFailure(error) {
  const code = typeof error?.code === 'string' ? error.code : '';
  if (code === 'HTTP_TIMEOUT') {
    return searchFailed('transport_timeout');
  }
  if (code === 'HTTP_BODY_TOO_LARGE') {
    return searchFailed('response_too_large');
  }
  if (code.startsWith('HTTP_REDIRECT_') || code === 'HTTP_TOO_MANY_REDIRECTS' || code === 'HTTP_ORIGIN_NOT_ALLOWED') {
    return searchFailed('redirect_invalid');
  }
  return searchFailed('transport_error');
}

function hasTurnoOptions(html) {
  const $ = load(String(html ?? ''));
  return $('select#turno, select[id$="_cboTurno"], select[name$="$cboTurno"]')
    .first()
    .find('option')
    .length > 0;
}

function webformsFailure(error) {
  const code = typeof error?.code === 'string' ? error.code : '';
  const known = {
    WEBFORMS_FORM_MISSING: 'form_missing',
    WEBFORMS_SUBMIT_MISSING: 'submit_missing',
    WEBFORMS_MATERIA_MISSING: 'materia_missing',
    WEBFORMS_OFRECIMIENTO_MISSING: 'ofrecimiento_missing',
    WEBFORMS_TURNO_MISSING: 'turno_missing',
  };
  return searchFailed(known[code] ?? 'form_invalid');
}

function isDeltaResponse(response) {
  return response.contentType.toLowerCase().startsWith('text/plain');
}

function parseVerifiedBody(response, limits) {
  if (!isDeltaResponse(response)) {
    if (!response.contentType.toLowerCase().includes('text/html')) {
      return { failure: searchFailed('response_invalid') };
    }
    return { html: response.body };
  }

  const parsed = parseDeltaResponse(response.body, {
    maxChars: limits.maxDeltaChars,
    maxNodes: limits.maxDeltaNodes,
  });
  if (parsed.status !== 'parsed') {
    return {
      failure: searchFailed(DELTA_FAILURE_REASONS[parsed.reason] ?? 'delta_malformed'),
    };
  }
  if (parsed.updatePanels.length === 0) {
    return { failure: searchFailed('delta_missing_panel') };
  }
  return { html: parsed.updatePanels.map((panel) => panel.html).join('') };
}

/**
 * Runs one browserless WebForms search using an already-scoped HTTP session.
 * `startUrl` is mandatory and never falls back to process configuration.
 *
 * @param {{ request: (url: string | URL, options?: object) => Promise<{status:number,body:string,url:string,contentType:string}> }} session
 * @param {unknown} filtros
 * @param {{ startUrl: string, limits?: { timeoutMs?: number, maxBodyBytes?: number, maxDeltaChars?: number, maxDeltaNodes?: number } }} options
 * @returns {Promise<{status:'invalid_credentials'|'rate_limited'|'stale_start_url'}|{status:'search_failed',reason:string}|{status:'verified',html:string,materiaNombre?:string}>}
 */
export async function runHttpSearch(session, filtros, { startUrl, limits = {} } = {}) {
  const parsedFiltros = FiltrosSchema.parse(filtros);
  if (!session || typeof session.request !== 'function') {
    return searchFailed('transport_unavailable');
  }
  if (typeof startUrl !== 'string' || startUrl.length === 0) {
    return searchFailed('start_url_missing');
  }

  let initialResponse;
  try {
    initialResponse = await session.request(startUrl, {
      timeoutMs: limits.timeoutMs,
      maxBodyBytes: limits.maxBodyBytes,
    });
  } catch (error) {
    return transportFailure(error);
  }

  const initialStatus = statusOutcome(initialResponse.status);
  if (initialStatus) {
    return initialStatus;
  }
  if (initialResponse.status < 200 || initialResponse.status >= 300) {
    return searchFailed('http_status_error');
  }
  if (!hasTurnoOptions(initialResponse.body)) {
    return { status: 'stale_start_url' };
  }

  let searchForm;
  try {
    searchForm = buildSearchPayload(initialResponse.body, parsedFiltros);
  } catch (error) {
    return webformsFailure(error);
  }

  // The approved capture accepts a conventional full form POST. The delta
  // parser remains active for defensive compatibility if the server elects
  // to return a Microsoft AJAX response despite that request mode.
  if (searchForm.postbackModeAccepted !== 'accepted') {
    return searchFailed('postback_mode_unsupported');
  }

  let postUrl;
  try {
    postUrl = new URL(searchForm.formAction, initialResponse.url);
  } catch {
    return searchFailed('form_action_invalid');
  }

  const effectiveLimits = {
    maxBodyBytes: limits.maxBodyBytes ?? searchForm.derivedLimits.maxBodyBytes,
    maxDeltaChars: limits.maxDeltaChars ?? searchForm.derivedLimits.maxDeltaChars,
    maxDeltaNodes: limits.maxDeltaNodes ?? searchForm.derivedLimits.maxDeltaNodes,
  };

  let postResponse;
  try {
    postResponse = await session.request(postUrl, {
      method: 'POST',
      timeoutMs: limits.timeoutMs,
      maxBodyBytes: effectiveLimits.maxBodyBytes,
      headers: {
        accept: 'text/html, text/plain;q=0.9',
        'content-type': searchForm.requestContract.contentType,
        origin: postUrl.origin,
        referer: initialResponse.url,
      },
      body: searchForm.payload,
    });
  } catch (error) {
    return transportFailure(error);
  }

  const postStatus = statusOutcome(postResponse.status);
  if (postStatus) {
    return postStatus;
  }
  if (postResponse.status < 200 || postResponse.status >= 300) {
    return searchFailed('http_status_error');
  }

  const parsedBody = parseVerifiedBody(postResponse, effectiveLimits);
  if (parsedBody.failure) {
    return parsedBody.failure;
  }
  const reflectedState = extractReflectedSearchState(parsedBody.html, parsedFiltros.materiaCodigo);
  if (!verifyPostbackMatchesQuery(reflectedState, parsedFiltros)) {
    return searchFailed('postback_mismatch');
  }

  const result = { status: 'verified', html: parsedBody.html };
  const materiaNombre = reflectedState.materiaNombre ?? searchForm.materiaNombre;
  if (materiaNombre) {
    result.materiaNombre = materiaNombre;
  }
  return result;
}
