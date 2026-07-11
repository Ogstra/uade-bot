import { test } from 'node:test';
import assert from 'node:assert/strict';
import { verifyPostbackMatchesQuery } from './search.js';

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
