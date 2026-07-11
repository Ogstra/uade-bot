import { hkdfSync, randomBytes, createCipheriv, createDecipheriv } from 'node:crypto';

// D-13: every user's encryption key is derived from the single
// CREDENTIALS_MASTER_KEY via HKDF, salted with that user's own Discord id.
// A single leaked derived key therefore never exposes any other user's
// credentials — only the master key itself would.
const HKDF_INFO = Buffer.from('uade-bot-credentials-v1', 'utf8');
const GCM_IV_LENGTH = 12;

// D-12: master-key rotation support (a secondary "previous key" fallback)
// is explicitly deferred. If rotation is ever needed, the documented plan
// is asking users to re-enter their credentials once — not code added to
// this module.

/**
 * Derives this Discord user's 32-byte AES-256 key from the shared master
 * key via HKDF-SHA256. `masterKeyHex` is a function parameter only — never
 * assigned to a module-level variable, never logged.
 *
 * @param {string} masterKeyHex - 64-character hex-encoded 32-byte master key
 * @param {string} discordUserId
 * @returns {Buffer} 32-byte derived key
 */
export function deriveUserKey(masterKeyHex, discordUserId) {
  const derived = hkdfSync(
    'sha256',
    Buffer.from(masterKeyHex, 'hex'),
    Buffer.from(discordUserId, 'utf8'),
    HKDF_INFO,
    32,
  );

  return Buffer.from(derived);
}

/**
 * Encrypts the three UADE values together as a single JSON payload under
 * AES-256-GCM, using a fresh random IV per call (D-10). None of
 * `masterKeyHex`/the plaintext values are ever assigned to a module-level
 * variable or logged — they exist only for the duration of this call.
 *
 * @param {string} masterKeyHex
 * @param {string} discordUserId
 * @param {{ uadeUsername: string, uadePassword: string, uadeStartUrl: string }} values
 * @returns {{ ciphertext: string, iv: string, authTag: string }} base64-encoded fields
 */
export function encryptCredentials(masterKeyHex, discordUserId, { uadeUsername, uadePassword, uadeStartUrl }) {
  const key = deriveUserKey(masterKeyHex, discordUserId);
  const iv = randomBytes(GCM_IV_LENGTH);
  const cipher = createCipheriv('aes-256-gcm', key, iv);

  const plaintext = JSON.stringify({ uadeUsername, uadePassword, uadeStartUrl });
  const ciphertext = Buffer.concat([cipher.update(plaintext, 'utf8'), cipher.final()]);
  const authTag = cipher.getAuthTag();

  return {
    ciphertext: ciphertext.toString('base64'),
    iv: iv.toString('base64'),
    authTag: authTag.toString('base64'),
  };
}

/**
 * Decrypts a `{ ciphertext, iv, authTag }` triple produced by
 * `encryptCredentials` back into the original three UADE values. Throws if
 * `discordUserId` doesn't match the id used to encrypt (derives a different
 * key, so AES-GCM auth-tag verification fails) or if the ciphertext/authTag
 * has been tampered with.
 *
 * @param {string} masterKeyHex
 * @param {string} discordUserId
 * @param {{ ciphertext: string, iv: string, authTag: string }} cipher
 * @returns {{ uadeUsername: string, uadePassword: string, uadeStartUrl: string }}
 */
export function decryptCredentials(masterKeyHex, discordUserId, { ciphertext, iv, authTag }) {
  const key = deriveUserKey(masterKeyHex, discordUserId);
  const decipher = createDecipheriv('aes-256-gcm', key, Buffer.from(iv, 'base64'));
  decipher.setAuthTag(Buffer.from(authTag, 'base64'));

  const plaintext = Buffer.concat([
    decipher.update(Buffer.from(ciphertext, 'base64')),
    decipher.final(),
  ]);

  return JSON.parse(plaintext.toString('utf8'));
}
