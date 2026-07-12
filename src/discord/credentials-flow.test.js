import assert from 'node:assert/strict';
import { randomBytes } from 'node:crypto';
import { test } from 'node:test';
import { createDatabase } from '../db/database.js';
import { getCredentials } from '../db/credentials.repository.js';
import { upsertUser } from '../db/users.repository.js';
import { decryptCredentials, encryptCredentials } from '../crypto/credentials-crypto.js';
import {
  collectCredentialValues,
  saveCredentialValues,
  rotateCredentialValues,
} from './credentials-flow.js';

function createDm(values) {
  const sent = [];
  const queue = [...values];
  return {
    sent,
    async send(message) {
      sent.push(message);
    },
    async awaitMessages() {
      const content = queue.shift();
      if (content === undefined) {
        return { first: () => null };
      }
      return { first: () => ({ content }) };
    },
  };
}

test('collectCredentialValues asks username, password, and start URL sequentially', async () => {
  const dm = createDm(['usuario', 'password', 'https://inscripcionespia.uade.edu.ar/x?param=abc']);

  const values = await collectCredentialValues(dm);

  assert.deepEqual(values, {
    uadeUsername: 'usuario',
    uadePassword: 'password',
    uadeStartUrl: 'https://inscripcionespia.uade.edu.ar/x?param=abc',
  });
  assert.equal(dm.sent.length, 3);
  assert.match(dm.sent[0], /usuario/i);
  assert.match(dm.sent[1], /password/i);
  assert.match(dm.sent[2], /link/i);
});

test('saveCredentialValues encrypts before persistence and raw DB row has no plaintext', () => {
  const db = createDatabase(':memory:');
  try {
    const masterKey = randomBytes(32).toString('hex');
    const values = {
      uadeUsername: 'usuario-secreto',
      uadePassword: 'password-secreta',
      uadeStartUrl: 'https://inscripcionespia.uade.edu.ar/x?param=secreto',
    };
    upsertUser(db, 'user-1');

    saveCredentialValues(db, { discordUserId: 'user-1', masterKey, values });

    const raw = JSON.stringify(db.prepare('SELECT * FROM credentials WHERE discord_user_id = ?').get('user-1'));
    assert.equal(raw.includes(values.uadeUsername), false);
    assert.equal(raw.includes(values.uadePassword), false);
    assert.equal(raw.includes(values.uadeStartUrl), false);
    assert.deepEqual(decryptCredentials(masterKey, 'user-1', getCredentials(db, 'user-1')), values);
  } finally {
    db.close();
  }
});

test('rotateCredentialValues supports password-only and start-url-only updates', () => {
  const db = createDatabase(':memory:');
  try {
    const masterKey = randomBytes(32).toString('hex');
    upsertUser(db, 'user-1');
    saveCredentialValues(db, {
      discordUserId: 'user-1',
      masterKey,
      values: {
        uadeUsername: 'old-user',
        uadePassword: 'old-pass',
        uadeStartUrl: 'https://inscripcionespia.uade.edu.ar/x?param=old',
      },
    });

    rotateCredentialValues(db, {
      discordUserId: 'user-1',
      masterKey,
      updates: { uadeUsername: 'new-user', uadePassword: 'new-pass' },
    });
    assert.deepEqual(decryptCredentials(masterKey, 'user-1', getCredentials(db, 'user-1')), {
      uadeUsername: 'new-user',
      uadePassword: 'new-pass',
      uadeStartUrl: 'https://inscripcionespia.uade.edu.ar/x?param=old',
    });

    rotateCredentialValues(db, {
      discordUserId: 'user-1',
      masterKey,
      updates: { uadeStartUrl: 'https://inscripcionespia.uade.edu.ar/x?param=new' },
    });
    assert.deepEqual(decryptCredentials(masterKey, 'user-1', getCredentials(db, 'user-1')), {
      uadeUsername: 'new-user',
      uadePassword: 'new-pass',
      uadeStartUrl: 'https://inscripcionespia.uade.edu.ar/x?param=new',
    });
  } finally {
    db.close();
  }
});
