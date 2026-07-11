import { test } from 'node:test';
import assert from 'node:assert/strict';
import { randomBytes } from 'node:crypto';
import { deriveUserKey, encryptCredentials, decryptCredentials } from './credentials-crypto.js';

const MASTER_KEY = randomBytes(32).toString('hex');

test('encryptCredentials + decryptCredentials round-trips the three UADE values', () => {
  const original = { uadeUsername: 'u', uadePassword: 'p', uadeStartUrl: 'https://x/y' };

  const cipher = encryptCredentials(MASTER_KEY, 'user-a', original);
  const decrypted = decryptCredentials(MASTER_KEY, 'user-a', cipher);

  assert.deepEqual(decrypted, original);
});

test('deriveUserKey produces different keys for different discordUserIds from the same master key', () => {
  const keyA = deriveUserKey(MASTER_KEY, 'user-a');
  const keyB = deriveUserKey(MASTER_KEY, 'user-b');

  assert.equal(keyA.length, 32);
  assert.equal(keyB.length, 32);
  assert.notEqual(keyA.toString('hex'), keyB.toString('hex'));
});

test('decryptCredentials throws when the discordUserId used to derive the key is wrong', () => {
  const cipher = encryptCredentials(MASTER_KEY, 'user-a', {
    uadeUsername: 'u',
    uadePassword: 'p',
    uadeStartUrl: 'https://x/y',
  });

  assert.throws(() => decryptCredentials(MASTER_KEY, 'user-b', cipher));
});

test('two encryptCredentials calls for the same values produce different ciphertext/iv (random IV per call)', () => {
  const original = { uadeUsername: 'u', uadePassword: 'p', uadeStartUrl: 'https://x/y' };

  const first = encryptCredentials(MASTER_KEY, 'user-a', original);
  const second = encryptCredentials(MASTER_KEY, 'user-a', original);

  assert.notEqual(first.iv, second.iv);
  assert.notEqual(first.ciphertext, second.ciphertext);
});
