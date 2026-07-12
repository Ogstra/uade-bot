import assert from 'node:assert/strict';
import { test } from 'node:test';
import { createDatabase } from './database.js';
import { listCommandUsageByUser, logCommandUsage } from './command-log.repository.js';

test('listCommandUsageByUser returns an empty array for a user with no logged commands', () => {
  const db = createDatabase(':memory:');
  try {
    assert.deepEqual(listCommandUsageByUser(db, 'user-1'), []);
  } finally {
    db.close();
  }
});

test('logCommandUsage records a command and listCommandUsageByUser returns it, newest first', () => {
  const db = createDatabase(':memory:');
  try {
    logCommandUsage(db, { discordUserId: 'user-1', commandName: 'buscar', guildId: 'guild-1' });
    logCommandUsage(db, { discordUserId: 'user-1', commandName: 'estado', guildId: 'guild-1' });
    logCommandUsage(db, { discordUserId: 'user-2', commandName: 'buscar', guildId: 'guild-1' });

    const rows = listCommandUsageByUser(db, 'user-1');

    assert.equal(rows.length, 2);
    assert.equal(rows[0].commandName, 'estado');
    assert.equal(rows[1].commandName, 'buscar');
    assert.equal(rows[0].guildId, 'guild-1');
    assert.ok(rows[0].createdAt > 0);
  } finally {
    db.close();
  }
});

test('logCommandUsage defaults guildId to null for DM-originated commands', () => {
  const db = createDatabase(':memory:');
  try {
    logCommandUsage(db, { discordUserId: 'user-1', commandName: 'credenciales' });

    const [row] = listCommandUsageByUser(db, 'user-1');

    assert.equal(row.guildId, null);
  } finally {
    db.close();
  }
});

test('listCommandUsageByUser respects the limit option', () => {
  const db = createDatabase(':memory:');
  try {
    for (let i = 0; i < 5; i += 1) {
      logCommandUsage(db, { discordUserId: 'user-1', commandName: 'estado', guildId: 'guild-1' });
    }

    assert.equal(listCommandUsageByUser(db, 'user-1', { limit: 2 }).length, 2);
  } finally {
    db.close();
  }
});
