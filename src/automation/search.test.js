import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  extractMateriaNombreFromCells,
  verifyPostbackMatchesQuery,
  resolveTurnoOptionValue,
  isStaleStartUrlSignal,
  navigateToStartUrl,
} from './search.js';

const baseFiltros = {
  materiaCodigo: '3.1.050',
  ofrecimiento: 'curricular',
  turno: 'M',
  dias: ['LU', 'MI'],
  sedesExcluidas: [],
};

test('verifyPostbackMatchesQuery returns true when reflected state matches the submitted filtros', () => {
  const reflectedState = {
    materiaCodigo: '3.1.050',
    ofrecimiento: 'curricular',
    turno: 'M',
    dias: ['MI', 'LU'], // order-independent
  };

  assert.equal(verifyPostbackMatchesQuery(reflectedState, baseFiltros), true);
});

test('verifyPostbackMatchesQuery returns false when the reflected materia does not match (stale/wrong fragment)', () => {
  const reflectedState = {
    materiaCodigo: '3.1.099',
    ofrecimiento: 'curricular',
    turno: 'M',
    dias: ['LU', 'MI'],
  };

  assert.equal(verifyPostbackMatchesQuery(reflectedState, baseFiltros), false);
});

test('verifyPostbackMatchesQuery returns false when the reflected turno does not match', () => {
  const reflectedState = {
    materiaCodigo: '3.1.050',
    ofrecimiento: 'curricular',
    turno: 'T',
    dias: ['LU', 'MI'],
  };

  assert.equal(verifyPostbackMatchesQuery(reflectedState, baseFiltros), false);
});

test('verifyPostbackMatchesQuery returns false when the reflected ofrecimiento does not match', () => {
  const reflectedState = {
    materiaCodigo: '3.1.050',
    ofrecimiento: 'optativa',
    turno: 'M',
    dias: ['LU', 'MI'],
  };

  assert.equal(verifyPostbackMatchesQuery(reflectedState, baseFiltros), false);
});

test('verifyPostbackMatchesQuery returns false when reflected dias are a subset (partial postback apply)', () => {
  const reflectedState = {
    materiaCodigo: '3.1.050',
    ofrecimiento: 'curricular',
    turno: 'M',
    dias: ['LU'],
  };

  assert.equal(verifyPostbackMatchesQuery(reflectedState, baseFiltros), false);
});

test('verifyPostbackMatchesQuery returns false for a null/missing reflected state (error fragment, not a valid empty result)', () => {
  assert.equal(verifyPostbackMatchesQuery(null, baseFiltros), false);
});

test('verifyPostbackMatchesQuery treats turno case/accent-insensitively (live select reflects uppercase, filtros carries the human-typed label)', () => {
  const reflectedState = {
    materiaCodigo: '3.4.219',
    ofrecimiento: 'curricular',
    turno: 'MAÑANA',
    dias: ['MI'],
  };

  const filtros = { ...baseFiltros, materiaCodigo: '3.4.219', turno: 'mañana', dias: ['MI'] };

  assert.equal(verifyPostbackMatchesQuery(reflectedState, filtros), true);
});

test('resolveTurnoOptionValue finds the option value by case/accent-insensitive label match (live values are opaque numeric ids)', () => {
  const options = [
    { value: '-1', text: '' },
    { value: '10152', text: 'MAÑANA' },
    { value: '10153', text: 'TARDE' },
    { value: '10154', text: 'NOCHE' },
  ];

  assert.equal(resolveTurnoOptionValue(options, 'mañana'), '10152');
  assert.equal(resolveTurnoOptionValue(options, 'MAÑANA'), '10152');
  assert.equal(resolveTurnoOptionValue(options, 'tarde'), '10153');
});

test('resolveTurnoOptionValue throws with the available options listed when no label matches', () => {
  const options = [{ value: '10152', text: 'MAÑANA' }];

  assert.throws(() => resolveTurnoOptionValue(options, 'inexistente'), /turno "inexistente" not found among available options: MAÑANA/);
});

test('isStaleStartUrlSignal returns true when the turno combo has zero options (D-07 stale-start-URL signal)', () => {
  assert.equal(isStaleStartUrlSignal(0), true);
});

test('isStaleStartUrlSignal returns false when the turno combo has any options, including a single placeholder', () => {
  assert.equal(isStaleStartUrlSignal(5), false);
  assert.equal(isStaleStartUrlSignal(1), false);
});

test('extractMateriaNombreFromCells returns the name after the materia code', () => {
  assert.equal(
    extractMateriaNombreFromCells(['5', '3.4.219', 'Ingenieria de Software'], '3.4.219'),
    'Ingenieria de Software',
  );
  assert.equal(
    extractMateriaNombreFromCells(['53.1.050', 'Fisica II'], '3.1.050'),
    'Fisica II',
  );
  assert.equal(extractMateriaNombreFromCells(['5', '3.1.099'], '3.1.050'), null);
});

function fakePage(gotoImpl) {
  return { goto: gotoImpl };
}

test('navigateToStartUrl succeeds on the first try without retrying', async () => {
  let calls = 0;
  const page = fakePage(async () => {
    calls += 1;
    return { status: () => 200 };
  });
  const sleeps = [];

  const result = await navigateToStartUrl(page, 'https://example.com', { sleepFn: async (ms) => sleeps.push(ms) });

  assert.equal(calls, 1);
  assert.deepEqual(sleeps, []);
  assert.equal(result.response.status(), 200);
});

test('navigateToStartUrl retries once after a transient failure and succeeds', async () => {
  let calls = 0;
  const page = fakePage(async () => {
    calls += 1;
    if (calls === 1) {
      throw new Error('net::ERR_CONNECTION_RESET at https://example.com');
    }
    return { status: () => 200 };
  });
  const sleeps = [];

  const result = await navigateToStartUrl(page, 'https://example.com', {
    retryDelayMs: 2000,
    sleepFn: async (ms) => sleeps.push(ms),
  });

  assert.equal(calls, 2);
  assert.deepEqual(sleeps, [2000]);
  assert.equal(result.response.status(), 200);
});

test('navigateToStartUrl returns the error when both the first attempt and the retry fail', async () => {
  let calls = 0;
  const page = fakePage(async () => {
    calls += 1;
    throw new Error('net::ERR_CONNECTION_RESET at https://example.com');
  });

  const result = await navigateToStartUrl(page, 'https://example.com', { sleepFn: async () => {} });

  assert.equal(calls, 2);
  assert.match(result.error.message, /ERR_CONNECTION_RESET/);
});

test('navigateToStartUrl does not retry an auth-challenge error (retrying the same credentials won\'t fix it)', async () => {
  let calls = 0;
  const page = fakePage(async () => {
    calls += 1;
    throw new Error('401 unauthorized at https://example.com');
  });
  const sleeps = [];

  const result = await navigateToStartUrl(page, 'https://example.com', { sleepFn: async (ms) => sleeps.push(ms) });

  assert.equal(calls, 1);
  assert.deepEqual(sleeps, []);
  assert.match(result.error.message, /401/);
});
