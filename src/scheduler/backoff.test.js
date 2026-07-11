import { test } from 'node:test';
import assert from 'node:assert/strict';
import { nextBackoffState, BACKOFF_SEQUENCE_MS } from './backoff.js';

test('nextBackoffState maps an invalid_credentials signal to needs_credentials, regardless of currentState', () => {
  const result = nextBackoffState({ currentState: { reason: 'none' }, signal: 'invalid_credentials' });
  assert.deepEqual(result, { reason: 'needs_credentials' });
});

test('nextBackoffState lets a later signal override an earlier pause reason (most recent poll result is authoritative)', () => {
  const result = nextBackoffState({ currentState: { reason: 'needs_credentials' }, signal: 'stale_start_url' });
  assert.deepEqual(result, { reason: 'needs_new_start_url' });
});

test('nextBackoffState always clears any prior pause on a success signal', () => {
  const fromCredentialsPause = nextBackoffState({ currentState: { reason: 'needs_credentials' }, signal: 'success' });
  assert.deepEqual(fromCredentialsPause, { reason: 'none' });

  const fromRateLimitedPause = nextBackoffState({
    currentState: { reason: 'rate_limited', backoffAttempt: 3, resumeAt: Date.now() + 300000 },
    signal: 'success',
  });
  assert.deepEqual(fromRateLimitedPause, { reason: 'none' });
});

test('three consecutive rate_limited signals from a fresh account follow the 1/2/5-minute backoff sequence', () => {
  const before = Date.now();

  const first = nextBackoffState({ currentState: { reason: 'none' }, signal: 'rate_limited' });
  assert.equal(first.reason, 'rate_limited');
  assert.equal(first.backoffAttempt, 1);
  assert.ok(first.resumeAt - before >= BACKOFF_SEQUENCE_MS[0]);
  assert.ok(first.resumeAt - before < BACKOFF_SEQUENCE_MS[0] + 5000);

  const second = nextBackoffState({ currentState: first, signal: 'rate_limited' });
  assert.equal(second.backoffAttempt, 2);
  assert.ok(second.resumeAt - before >= BACKOFF_SEQUENCE_MS[1]);
  assert.ok(second.resumeAt - before < BACKOFF_SEQUENCE_MS[1] + 5000);

  const third = nextBackoffState({ currentState: second, signal: 'rate_limited' });
  assert.equal(third.backoffAttempt, 3);
  assert.ok(third.resumeAt - before >= BACKOFF_SEQUENCE_MS[2]);
  assert.ok(third.resumeAt - before < BACKOFF_SEQUENCE_MS[2] + 5000);
});

test('a 5th consecutive rate_limited signal caps the delay at the last BACKOFF_SEQUENCE_MS entry instead of indexing out of bounds', () => {
  const before = Date.now();

  let state = { reason: 'none' };
  for (let i = 0; i < 4; i += 1) {
    state = nextBackoffState({ currentState: state, signal: 'rate_limited' });
  }
  assert.equal(state.backoffAttempt, 4);

  const fifth = nextBackoffState({ currentState: state, signal: 'rate_limited' });
  assert.equal(fifth.backoffAttempt, 5);
  const maxDelay = BACKOFF_SEQUENCE_MS[BACKOFF_SEQUENCE_MS.length - 1];
  assert.ok(fifth.resumeAt - before >= maxDelay);
  assert.ok(fifth.resumeAt - before < maxDelay + 5000);
});

test('nextBackoffState throws for an unrecognized signal', () => {
  assert.throws(() => nextBackoffState({ currentState: { reason: 'none' }, signal: 'not_a_real_signal' }));
});
