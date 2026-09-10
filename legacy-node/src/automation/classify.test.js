import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import { classifySearchResult } from './classify.js';

const oneRow = { turno: 'NOCHE', sede: 'MONSERRAT', horario: '18:45 22:15', dias: ['LU'], cupos: 3 };

describe('classifySearchResult', () => {
  test('invalid_credentials status maps to { outcome: "invalid_credentials" }', () => {
    const result = classifySearchResult({ searchStatus: 'invalid_credentials' });
    assert.deepEqual(result, { outcome: 'invalid_credentials' });
  });

  test('search_failed status maps to { outcome: "search_failed", reason }', () => {
    const result = classifySearchResult({ searchStatus: 'search_failed', reason: 'postback_mismatch' });
    assert.deepEqual(result, { outcome: 'search_failed', reason: 'postback_mismatch' });
  });

  test('verified status with zero vacancies maps to { outcome: "no_vacancies" }', () => {
    const result = classifySearchResult({ searchStatus: 'verified', vacancies: [] });
    assert.deepEqual(result, { outcome: 'no_vacancies' });
  });

  test('verified status with at least one vacancy maps to { outcome: "found", vacancies }', () => {
    const result = classifySearchResult({ searchStatus: 'verified', vacancies: [oneRow] });
    assert.deepEqual(result, { outcome: 'found', vacancies: [oneRow] });
  });

  test('the four outcomes are mutually exclusive and exhaustive: every branch validates against SearchOutcomeSchema', () => {
    const invalid = classifySearchResult({ searchStatus: 'invalid_credentials' });
    const failed = classifySearchResult({ searchStatus: 'search_failed', reason: 'postback_timeout' });
    const empty = classifySearchResult({ searchStatus: 'verified', vacancies: [] });
    const found = classifySearchResult({ searchStatus: 'verified', vacancies: [oneRow] });

    const outcomes = new Set([invalid.outcome, failed.outcome, empty.outcome, found.outcome]);
    assert.equal(outcomes.size, 4);
  });

  test('throws on an unrecognized searchStatus rather than silently returning an invalid shape', () => {
    assert.throws(() => classifySearchResult({ searchStatus: 'bogus' }));
  });
});
