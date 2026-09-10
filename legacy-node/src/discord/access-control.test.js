import assert from 'node:assert/strict';
import { test } from 'node:test';
import { isFromAuthorizedGuild, isGuildInteraction } from './access-control.js';

test('isFromAuthorizedGuild() matches only the exact authorized guild id', () => {
  assert.equal(isFromAuthorizedGuild({ guildId: 'guild-1' }, 'guild-1'), true);
  assert.equal(isFromAuthorizedGuild({ guildId: 'guild-2' }, 'guild-1'), false);
  assert.equal(isFromAuthorizedGuild({ guildId: null }, 'guild-1'), false);
  assert.equal(isFromAuthorizedGuild({}, 'guild-1'), false);
});

test('isGuildInteraction() detects guild-based interactions only', () => {
  assert.equal(isGuildInteraction({ guildId: 'guild-1' }), true);
  assert.equal(isGuildInteraction({ guildId: null }), false);
  assert.equal(isGuildInteraction({}), false);
});
