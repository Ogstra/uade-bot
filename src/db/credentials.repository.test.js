import { test } from 'node:test';
import assert from 'node:assert/strict';
import { randomBytes } from 'node:crypto';
import { createDatabase } from './database.js';
import { upsertUser } from './users.repository.js';
import { upsertCredentials, getCredentials } from './credentials.repository.js';
import { encryptCredentials, decryptCredentials } from '../crypto/credentials-crypto.js';

test('credentials table never stores plaintext and round-trips through encrypt/store/fetch/decrypt', () => {
  const db = createDatabase(':memory:');

  try {
    const masterKey = randomBytes(32).toString('hex');
    const discordUserId = 'user-integration';
    upsertUser(db, discordUserId);

    const original = {
      uadeUsername: 'secretUsername',
      uadePassword: 'secretPassword123',
      uadeStartUrl: 'https://inscripcionespia.uade.edu.ar/x?param=abc123',
    };

    const cipher = encryptCredentials(masterKey, discordUserId, original);
    upsertCredentials(db, { discordUserId, ...cipher });

    // Raw SQL row, bypassing the repository layer entirely.
    const rawRow = db.prepare('SELECT * FROM credentials WHERE discord_user_id = ?').get(discordUserId);
    const rawRowText = JSON.stringify(rawRow);

    assert.equal(rawRowText.includes(original.uadeUsername), false);
    assert.equal(rawRowText.includes(original.uadePassword), false);
    assert.equal(rawRowText.includes(original.uadeStartUrl), false);

    const stored = getCredentials(db, discordUserId);
    const decrypted = decryptCredentials(masterKey, discordUserId, stored);

    assert.deepEqual(decrypted, original);
  } finally {
    db.close();
  }
});
