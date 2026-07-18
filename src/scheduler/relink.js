import { withPlainContext } from '../automation/browser.js';
import { obtainStartUrl } from '../automation/sso-link.js';
import { rotateCredentialValues } from '../discord/credentials-flow.js';
import logger from '../logger.js';

/**
 * Attempts the exceptional SSO relink using the credentials already
 * decrypted by pollOnce. This module owns every browser/SSO import so the
 * routine HTTP poll path can stay browserless.
 *
 * A successful result rotates only the encrypted start URL and returns a
 * fixed status. MFA and all other failures preserve the existing manual
 * fallback without exposing credentials or the obtained URL.
 *
 * @param {import('better-sqlite3').Database} db
 * @param {import('zod').infer<typeof import('../schemas.js').SearchJobSchema>} job
 * @param {{ username: string, password: string, masterKey: string }} credentials
 * @param {{
 *   withPlainContextFn?: typeof withPlainContext,
 *   obtainStartUrlFn?: typeof obtainStartUrl,
 *   rotateCredentialValuesFn?: typeof rotateCredentialValues,
 * }} [deps]
 * @returns {Promise<{ status: 'success' } | { status: 'fallback' }>}
 */
export async function attemptAutoRelink(
  db,
  job,
  { username, password, masterKey },
  {
    withPlainContextFn = withPlainContext,
    obtainStartUrlFn = obtainStartUrl,
    rotateCredentialValuesFn = rotateCredentialValues,
  } = {},
) {
  try {
    const result = await withPlainContextFn((context) => obtainStartUrlFn(context, { username, password }));

    if (result.status === 'success') {
      rotateCredentialValuesFn(db, {
        discordUserId: job.discordUserId,
        masterKey,
        updates: { uadeStartUrl: result.startUrl },
      });
      logger.info({ event: 'auto_relink_applied', jobId: job.id }, 'Automatic SSO relink succeeded; uadeStartUrl rotated');
      return { status: 'success' };
    }

    const reason = result.status === 'mfa_required' ? 'mfa_required' : 'relink_failed';
    logger.info(
      { event: 'auto_relink_fallback', jobId: job.id, reason },
      'Automatic SSO relink did not succeed; falling back to the manual credential flow',
    );
    return { status: 'fallback' };
  } catch {
    logger.warn(
      { event: 'auto_relink_fallback', jobId: job.id, reason: 'relink_exception' },
      'Automatic SSO relink failed exceptionally; falling back to the manual credential flow',
    );
    return { status: 'fallback' };
  }
}
