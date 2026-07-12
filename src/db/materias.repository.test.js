import assert from 'node:assert/strict';
import { test } from 'node:test';
import { createDatabase } from './database.js';
import { getMateriaNombre, upsertMateriaNombre } from './materias.repository.js';

test('getMateriaNombre returns null for a code that was never cached', () => {
  const db = createDatabase(':memory:');
  try {
    assert.equal(getMateriaNombre(db, '3.1.050'), null);
  } finally {
    db.close();
  }
});

test('upsertMateriaNombre caches a name and getMateriaNombre returns it', () => {
  const db = createDatabase(':memory:');
  try {
    upsertMateriaNombre(db, '3.1.050', 'FISICA II');

    assert.equal(getMateriaNombre(db, '3.1.050'), 'FISICA II');
  } finally {
    db.close();
  }
});

test('upsertMateriaNombre overwrites a previously-cached name for the same codigo', () => {
  const db = createDatabase(':memory:');
  try {
    upsertMateriaNombre(db, '3.1.050', 'Nombre viejo');
    upsertMateriaNombre(db, '3.1.050', 'Nombre nuevo');

    assert.equal(getMateriaNombre(db, '3.1.050'), 'Nombre nuevo');
  } finally {
    db.close();
  }
});
