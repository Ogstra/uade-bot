import assert from 'node:assert/strict';
import { randomBytes } from 'node:crypto';
import { test } from 'node:test';
import { MessageFlags } from 'discord.js';
import { createDatabase } from '../../db/database.js';
import { getCredentials } from '../../db/credentials.repository.js';
import { upsertUser } from '../../db/users.repository.js';
import { decryptCredentials } from '../../crypto/credentials-crypto.js';
import { credencialesCommand } from './credenciales.js';
import { commandsByName } from './index.js';

// Prevents these tests from launching a real Playwright browser (the
// credential-save success path fires a fire-and-forget getBrowserFn() to
// pre-warm the shared browser -- see credentials-flow.js's warmBrowser).
async function noopGetBrowser() {
  return {};
}

// Fase 3.1: collectCredentialValues/runCredentialRotation now try to obtain
// the inscripción link automatically before ever asking for it by DM --
// stub withPlainContextFn/obtainStartUrlFn so these tests never launch a
// real browser or hit the real UADE/Microsoft site.
async function noopWithPlainContext(run) {
  return run({});
}

function fakeObtainStartUrlSuccess(startUrl) {
  return async () => ({ status: 'success', startUrl });
}

function fakeObtainStartUrlMfaRequired() {
  return async () => ({ status: 'mfa_required' });
}

function createInteraction({ userId = 'user-1', modo = 'todo', values = ['usuario', 'password'] } = {}) {
  const calls = [];
  const sent = [];
  const queue = [...values];
  const dm = {
    async send(message) {
      sent.push(message);
    },
    async awaitMessages() {
      const content = queue.shift();
      return { first: () => ({ content }) };
    },
  };
  return {
    user: {
      id: userId,
      async createDM() {
        return dm;
      },
    },
    options: {
      getString(name) {
        return name === 'modo' ? modo : null;
      },
    },
    async reply(payload) {
      calls.push(['reply', payload]);
    },
    async deferReply(payload) {
      calls.push(['deferReply', payload]);
    },
    async editReply(payload) {
      calls.push(['editReply', payload]);
    },
    get calls() {
      return calls;
    },
    get sent() {
      return sent;
    },
  };
}

test('/credenciales is registered and obtains the link automatically without asking for it', async () => {
  const db = createDatabase(':memory:');
  try {
    const masterKey = randomBytes(32).toString('hex');
    const interaction = createInteraction();

    assert.equal(commandsByName.get('credenciales'), credencialesCommand);
    await credencialesCommand.execute(interaction, {
      db,
      env: { CREDENTIALS_MASTER_KEY: masterKey },
      getBrowserFn: noopGetBrowser,
      withPlainContextFn: noopWithPlainContext,
      obtainStartUrlFn: fakeObtainStartUrlSuccess('https://inscripcionespia.uade.edu.ar/x?param=abc'),
    });

    assert.deepEqual(interaction.calls[0], ['deferReply', { flags: MessageFlags.Ephemeral }]);
    assert.equal(interaction.calls.at(-1)[0], 'editReply');
    assert.match(String(interaction.calls.at(-1)[1]), /guardadas/i);
    const decrypted = decryptCredentials(masterKey, 'user-1', getCredentials(db, 'user-1'));
    assert.deepEqual(decrypted, {
      uadeUsername: 'usuario',
      uadePassword: 'password',
      uadeStartUrl: 'https://inscripcionespia.uade.edu.ar/x?param=abc',
    });
    // Only 2 DM prompts (username, password) -- the link was never asked for.
    assert.equal(interaction.sent.length, 2);
    assert.equal(JSON.stringify(interaction.calls).includes('password'), false);
  } finally {
    db.close();
  }
});

test('/credenciales falls back to asking for the link by DM when auto-obtain hits MFA', async () => {
  const db = createDatabase(':memory:');
  try {
    const masterKey = randomBytes(32).toString('hex');
    const interaction = createInteraction({
      values: ['usuario', 'password', 'https://inscripcionespia.uade.edu.ar/x?param=manual'],
    });

    await credencialesCommand.execute(interaction, {
      db,
      env: { CREDENTIALS_MASTER_KEY: masterKey },
      getBrowserFn: noopGetBrowser,
      withPlainContextFn: noopWithPlainContext,
      obtainStartUrlFn: fakeObtainStartUrlMfaRequired(),
    });

    const decrypted = decryptCredentials(masterKey, 'user-1', getCredentials(db, 'user-1'));
    assert.equal(decrypted.uadeStartUrl, 'https://inscripcionespia.uade.edu.ar/x?param=manual');
    // 3 DM prompts this time: username, password, and the fallback link ask.
    assert.equal(interaction.sent.length, 3);
  } finally {
    db.close();
  }
});

test('/credenciales modo:usuario_password also (re-)obtains the link, fixing the bug where it was left missing', async () => {
  const db = createDatabase(':memory:');
  try {
    const masterKey = randomBytes(32).toString('hex');
    upsertUser(db, 'user-1');
    const interaction = createInteraction({ modo: 'usuario_password', values: ['nuevo-usuario', 'nuevo-password'] });

    await credencialesCommand.execute(interaction, {
      db,
      env: { CREDENTIALS_MASTER_KEY: masterKey },
      getBrowserFn: noopGetBrowser,
      withPlainContextFn: noopWithPlainContext,
      obtainStartUrlFn: fakeObtainStartUrlSuccess('https://inscripcionespia.uade.edu.ar/x?param=refreshed'),
    });

    const decrypted = decryptCredentials(masterKey, 'user-1', getCredentials(db, 'user-1'));
    assert.deepEqual(decrypted, {
      uadeUsername: 'nuevo-usuario',
      uadePassword: 'nuevo-password',
      uadeStartUrl: 'https://inscripcionespia.uade.edu.ar/x?param=refreshed',
    });
  } finally {
    db.close();
  }
});

test('/credenciales can update only the session link without echoing it (modo:link stays manual-only)', async () => {
  const db = createDatabase(':memory:');
  try {
    const masterKey = randomBytes(32).toString('hex');
    upsertUser(db, 'user-1');
    await credencialesCommand.execute(createInteraction(), {
      db,
      env: { CREDENTIALS_MASTER_KEY: masterKey },
      getBrowserFn: noopGetBrowser,
      withPlainContextFn: noopWithPlainContext,
      obtainStartUrlFn: fakeObtainStartUrlSuccess('https://inscripcionespia.uade.edu.ar/x?param=abc'),
    });

    const interaction = createInteraction({
      modo: 'link',
      values: ['https://inscripcionespia.uade.edu.ar/x?param=new'],
    });
    await credencialesCommand.execute(interaction, {
      db,
      env: { CREDENTIALS_MASTER_KEY: masterKey },
      getBrowserFn: noopGetBrowser,
      withPlainContextFn: noopWithPlainContext,
      obtainStartUrlFn: fakeObtainStartUrlSuccess('https://should-not-be-used.example/'),
    });

    assert.deepEqual(decryptCredentials(masterKey, 'user-1', getCredentials(db, 'user-1')), {
      uadeUsername: 'usuario',
      uadePassword: 'password',
      uadeStartUrl: 'https://inscripcionespia.uade.edu.ar/x?param=new',
    });
    assert.equal(JSON.stringify(interaction.calls).includes('param=new'), false);
  } finally {
    db.close();
  }
});
