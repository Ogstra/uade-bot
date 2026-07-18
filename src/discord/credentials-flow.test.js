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
  runFullCredentialOnboarding,
  runCredentialRotation,
} from './credentials-flow.js';

// Fase 3.1: collectCredentialValues tries to obtain the inscripción link
// automatically before ever asking for it by DM. Stub withPlainContextFn/
// obtainStartUrlFn in every test that exercises that path so none of them
// launch a real Playwright browser or hit the real UADE/Microsoft site.
async function noopWithPlainContext(run) {
  return run({});
}

function fakeObtainStartUrlSuccess(startUrl) {
  return async () => ({ status: 'success', startUrl });
}

function fakeObtainStartUrlMfaRequired() {
  return async () => ({ status: 'mfa_required' });
}

function createInteraction({ userId = 'user-1', dm, modo = null } = {}) {
  return {
    user: {
      id: userId,
      async createDM() {
        if (!dm) {
          throw new Error('no DM available');
        }
        return dm;
      },
    },
    options: {
      getString: (name) => (name === 'modo' ? modo : null),
    },
  };
}

// Simulates the DM channel containing BOTH the bot's own just-sent prompt
// (author.id === botUserId) and the queued human reply (author.id ===
// userId), so tests exercise whatever filter ask() passes to
// awaitMessages() -- without a filter, the bot's own echo is what a real
// Discord collector would see first, which is exactly the bug confirmed
// live 2026-07-12 (every prompt resolved instantly off its own text
// instead of waiting for the real reply).
function createDm(values, { userId = 'user-1', botUserId = 'bot-1' } = {}) {
  const sent = [];
  const queue = [...values];
  return {
    sent,
    async send(message) {
      sent.push(message);
    },
    async awaitMessages({ filter } = {}) {
      const botEcho = { author: { id: botUserId }, content: '(bot echo -- must never be collected as a reply)' };
      const content = queue.shift();
      const humanReply = content === undefined ? null : { author: { id: userId }, content };

      if (!filter) {
        return { first: () => botEcho };
      }

      const candidates = humanReply ? [botEcho, humanReply] : [botEcho];
      const matched = candidates.find((m) => filter(m));
      return { first: () => matched ?? null };
    },
  };
}

test('collectCredentialValues asks username and password, then obtains the link automatically (no third DM prompt)', async () => {
  const dm = createDm(['usuario', 'password']);

  const values = await collectCredentialValues(dm, {
    userId: 'user-1',
    withPlainContextFn: noopWithPlainContext,
    obtainStartUrlFn: fakeObtainStartUrlSuccess('https://inscripcionespia.uade.edu.ar/x?param=abc'),
  });

  assert.deepEqual(values, {
    uadeUsername: 'usuario',
    uadePassword: 'password',
    uadeStartUrl: 'https://inscripcionespia.uade.edu.ar/x?param=abc',
  });
  assert.equal(dm.sent.length, 2);
  assert.match(dm.sent[0], /usuario/i);
  assert.match(dm.sent[1], /password/i);
});

test('collectCredentialValues falls back to asking for the link by DM when auto-obtain hits MFA', async () => {
  const dm = createDm(['usuario', 'password', 'https://inscripcionespia.uade.edu.ar/x?param=manual']);

  const values = await collectCredentialValues(dm, {
    userId: 'user-1',
    withPlainContextFn: noopWithPlainContext,
    obtainStartUrlFn: fakeObtainStartUrlMfaRequired(),
  });

  assert.deepEqual(values, {
    uadeUsername: 'usuario',
    uadePassword: 'password',
    uadeStartUrl: 'https://inscripcionespia.uade.edu.ar/x?param=manual',
  });
  assert.equal(dm.sent.length, 3);
  assert.match(dm.sent[2], /link/i);
});

test('collectCredentialValues ignores the bot\'s own just-sent prompt and only accepts a reply from userId', async () => {
  // Forces the MFA fallback so the self-echo filter is exercised on all
  // three DM prompts (username, password, and the fallback link ask), not
  // just the first two.
  const dm = createDm(['usuario', 'password', 'https://inscripcionespia.uade.edu.ar/x?param=abc'], {
    userId: 'user-1',
    botUserId: 'bot-1',
  });

  const values = await collectCredentialValues(dm, {
    userId: 'user-1',
    withPlainContextFn: noopWithPlainContext,
    obtainStartUrlFn: fakeObtainStartUrlMfaRequired(),
  });

  assert.notEqual(values.uadeUsername, '(bot echo -- must never be collected as a reply)');
  assert.deepEqual(values, {
    uadeUsername: 'usuario',
    uadePassword: 'password',
    uadeStartUrl: 'https://inscripcionespia.uade.edu.ar/x?param=abc',
  });
});

test('collectCredentialValues times out rather than resolving off the bot\'s own message when no filter would match', async () => {
  // No human reply queued at all -- only the bot's own echo is "in the
  // channel". A correct filter must reject it and time out (never resolve
  // with the bot's own prompt text as the value).
  const dm = createDm([], { userId: 'user-1', botUserId: 'bot-1' });

  await assert.rejects(() => collectCredentialValues(dm, { userId: 'user-1', timeoutMs: 10 }), /credential_prompt_timeout/);
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

test('runFullCredentialOnboarding does not launch a browser after a successful save', async () => {
  const db = createDatabase(':memory:');
  try {
    const dm = createDm(['usuario', 'password']);
    const interaction = createInteraction({ dm });
    let warmCalls = 0;
    const getBrowserFn = async () => {
      warmCalls += 1;
      return {};
    };

    const result = await runFullCredentialOnboarding(interaction, {
      db,
      env: { CREDENTIALS_MASTER_KEY: randomBytes(32).toString('hex') },
      getBrowserFn,
      withPlainContextFn: noopWithPlainContext,
      obtainStartUrlFn: fakeObtainStartUrlSuccess('https://inscripcionespia.uade.edu.ar/x?param=abc'),
    });

    assert.equal(result.ok, true);
    assert.equal(warmCalls, 0);
  } finally {
    db.close();
  }
});

test('runFullCredentialOnboarding does not warm the browser when the DM cannot be opened', async () => {
  const interaction = createInteraction({ dm: null });
  let warmCalls = 0;
  const getBrowserFn = async () => {
    warmCalls += 1;
    return {};
  };

  const result = await runFullCredentialOnboarding(interaction, { getBrowserFn });

  assert.equal(result.ok, false);
  assert.equal(warmCalls, 0);
});

test('runFullCredentialOnboarding does not warm the browser when credential collection fails', async () => {
  const dm = createDm(['usuario']); // times out waiting for password/start URL
  const interaction = createInteraction({ dm });
  let warmCalls = 0;
  const getBrowserFn = async () => {
    warmCalls += 1;
    return {};
  };

  const result = await runFullCredentialOnboarding(interaction, {
    db: createDatabase(':memory:'),
    env: { CREDENTIALS_MASTER_KEY: randomBytes(32).toString('hex') },
    getBrowserFn,
  });

  assert.equal(result.ok, false);
  assert.equal(warmCalls, 0);
});

test('runFullCredentialOnboarding does not reject even if the background browser warm-up fails', async () => {
  const db = createDatabase(':memory:');
  try {
    const dm = createDm(['usuario', 'password']);
    const interaction = createInteraction({ dm });
    const getBrowserFn = async () => {
      throw new Error('browser launch failed');
    };

    const result = await runFullCredentialOnboarding(interaction, {
      db,
      env: { CREDENTIALS_MASTER_KEY: randomBytes(32).toString('hex') },
      getBrowserFn,
      withPlainContextFn: noopWithPlainContext,
      obtainStartUrlFn: fakeObtainStartUrlSuccess('https://inscripcionespia.uade.edu.ar/x?param=abc'),
    });

    assert.equal(result.ok, true);
  } finally {
    db.close();
  }
});

test('runCredentialRotation does not launch a browser after a successful update', async () => {
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

    const dm = createDm(['https://inscripcionespia.uade.edu.ar/x?param=new']);
    const interaction = createInteraction({ dm, modo: 'link' });
    let warmCalls = 0;
    const getBrowserFn = async () => {
      warmCalls += 1;
      return {};
    };

    const result = await runCredentialRotation(interaction, { db, env: { CREDENTIALS_MASTER_KEY: masterKey }, getBrowserFn });

    assert.equal(result.ok, true);
    assert.equal(warmCalls, 0);
  } finally {
    db.close();
  }
});

test('runCredentialRotation does not warm the browser when rotation fails', async () => {
  const db = createDatabase(':memory:');
  try {
    const dm = createDm([]); // no queued values -- ask() times out immediately
    const interaction = createInteraction({ dm, modo: 'link' });
    let warmCalls = 0;
    const getBrowserFn = async () => {
      warmCalls += 1;
      return {};
    };

    const result = await runCredentialRotation(interaction, {
      db,
      env: { CREDENTIALS_MASTER_KEY: randomBytes(32).toString('hex') },
      getBrowserFn,
    });

    assert.equal(result.ok, false);
    assert.equal(warmCalls, 0);
  } finally {
    db.close();
  }
});

test('runCredentialRotation modo:usuario_password also obtains the link, fixing an account left with no usable uadeStartUrl', async () => {
  const db = createDatabase(':memory:');
  try {
    const masterKey = randomBytes(32).toString('hex');
    upsertUser(db, 'user-1');
    // No pre-existing credentials row -- this account never had a link at
    // all, the exact scenario confirmed live 2026-07-12 to leave polls
    // failing with navigation_failed once rotateCredentialValues's
    // require-all-three-fields-for-a-fresh-account guard silently... didn't
    // apply, because uadeStartUrl was never asked for at all under the old
    // behavior.
    const dm = createDm(['nuevo-usuario', 'nuevo-password']);
    const interaction = createInteraction({ dm, modo: 'usuario_password' });

    const result = await runCredentialRotation(interaction, {
      db,
      env: { CREDENTIALS_MASTER_KEY: masterKey },
      getBrowserFn: async () => ({}),
      withPlainContextFn: noopWithPlainContext,
      obtainStartUrlFn: fakeObtainStartUrlSuccess('https://inscripcionespia.uade.edu.ar/x?param=refreshed'),
    });

    assert.equal(result.ok, true);
    assert.deepEqual(decryptCredentials(masterKey, 'user-1', getCredentials(db, 'user-1')), {
      uadeUsername: 'nuevo-usuario',
      uadePassword: 'nuevo-password',
      uadeStartUrl: 'https://inscripcionespia.uade.edu.ar/x?param=refreshed',
    });
  } finally {
    db.close();
  }
});

test('runFullCredentialOnboarding triggers onCredentialsUpdated with the discord user id after a successful save', async () => {
  const db = createDatabase(':memory:');
  try {
    const dm = createDm(['usuario', 'password']);
    const interaction = createInteraction({ dm });
    const calls = [];

    const result = await runFullCredentialOnboarding(interaction, {
      db,
      env: { CREDENTIALS_MASTER_KEY: randomBytes(32).toString('hex') },
      getBrowserFn: async () => ({}),
      withPlainContextFn: noopWithPlainContext,
      obtainStartUrlFn: fakeObtainStartUrlSuccess('https://inscripcionespia.uade.edu.ar/x?param=abc'),
      onCredentialsUpdated: (discordUserId) => calls.push(discordUserId),
    });

    assert.equal(result.ok, true);
    assert.deepEqual(calls, ['user-1']);
  } finally {
    db.close();
  }
});

test('runFullCredentialOnboarding does not call onCredentialsUpdated when the save fails', async () => {
  const dm = createDm(['usuario']); // times out waiting for password
  const interaction = createInteraction({ dm });
  const calls = [];

  const result = await runFullCredentialOnboarding(interaction, {
    db: createDatabase(':memory:'),
    env: { CREDENTIALS_MASTER_KEY: randomBytes(32).toString('hex') },
    getBrowserFn: async () => ({}),
    onCredentialsUpdated: (discordUserId) => calls.push(discordUserId),
  });

  assert.equal(result.ok, false);
  assert.deepEqual(calls, []);
});

test('runCredentialRotation triggers onCredentialsUpdated after a successful modo:link rotation', async () => {
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

    const dm = createDm(['https://inscripcionespia.uade.edu.ar/x?param=new']);
    const interaction = createInteraction({ dm, modo: 'link' });
    const calls = [];

    const result = await runCredentialRotation(interaction, {
      db,
      env: { CREDENTIALS_MASTER_KEY: masterKey },
      getBrowserFn: async () => ({}),
      onCredentialsUpdated: (discordUserId) => calls.push(discordUserId),
    });

    assert.equal(result.ok, true);
    assert.deepEqual(calls, ['user-1']);
  } finally {
    db.close();
  }
});

// Regression for the live 2026-07-13 bug: a manually-pasted modo:link value
// that isn't a real inscripcionespia.uade.edu.ar param= link (wrong domain,
// or not a link at all) used to save straight through, after which every
// poll failed with navigation_failed forever -- no pause, no DM, no
// self-healing (see askStartUrl's docstring in credentials-flow.js).
test('runCredentialRotation modo:link rejects a link on the wrong domain without saving it', async () => {
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

    const dm = createDm(['https://otro-sitio.example/no-es-el-link-correcto']);
    const interaction = createInteraction({ dm, modo: 'link' });

    const result = await runCredentialRotation(interaction, {
      db,
      env: { CREDENTIALS_MASTER_KEY: masterKey },
      getBrowserFn: async () => ({}),
    });

    assert.equal(result.ok, false);
    assert.match(result.message, /no parece un link de inscripcion valido/);
    assert.deepEqual(decryptCredentials(masterKey, 'user-1', getCredentials(db, 'user-1')), {
      uadeUsername: 'old-user',
      uadePassword: 'old-pass',
      uadeStartUrl: 'https://inscripcionespia.uade.edu.ar/x?param=old',
    });
  } finally {
    db.close();
  }
});

test('runCredentialRotation modo:link rejects a non-link reply without saving it', async () => {
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

    const dm = createDm(['no tengo el link ahora']);
    const interaction = createInteraction({ dm, modo: 'link' });

    const result = await runCredentialRotation(interaction, {
      db,
      env: { CREDENTIALS_MASTER_KEY: masterKey },
      getBrowserFn: async () => ({}),
    });

    assert.equal(result.ok, false);
    assert.deepEqual(
      decryptCredentials(masterKey, 'user-1', getCredentials(db, 'user-1')).uadeStartUrl,
      'https://inscripcionespia.uade.edu.ar/x?param=old',
    );
  } finally {
    db.close();
  }
});

test('collectCredentialValues rejects an invalid manually-pasted link in the MFA fallback path', async () => {
  const dm = createDm(['usuario', 'password', 'esto no es un link']);

  await assert.rejects(
    () =>
      collectCredentialValues(dm, {
        userId: 'user-1',
        withPlainContextFn: noopWithPlainContext,
        obtainStartUrlFn: fakeObtainStartUrlMfaRequired(),
      }),
    /invalid_start_url_link/,
  );
});
