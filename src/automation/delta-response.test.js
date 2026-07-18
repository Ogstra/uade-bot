import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { parseDeltaResponse } from './delta-response.js';

const fixtureUrl = (name) => new URL(`./__fixtures__/webforms/${name}`, import.meta.url);
const fixture = (name) => readFileSync(fileURLToPath(fixtureUrl(name)), 'utf8').trimEnd();

test('parses length-prefixed panels with Unicode and internal pipes exactly', () => {
  const result = parseDeltaResponse(fixture('delta-found.txt'));

  assert.deepEqual(result, {
    status: 'parsed',
    updatePanels: [{
      id: 'ctl00$UpdatePanelContenido',
      html: '<div>Vacantes: 2 | Física II – mañana</div>',
    }],
    hiddenFields: [{ name: '__VIEWSTATE', value: 'estado|válido' }],
  });
});

test('keeps valid empty and mismatched panel contents distinct for reflected-state verification', () => {
  const empty = parseDeltaResponse(fixture('delta-empty.txt'));
  const mismatch = parseDeltaResponse(fixture('delta-mismatch.txt'));

  assert.equal(empty.status, 'parsed');
  assert.equal(empty.updatePanels[0].html, '<div>Sin filas | búsqueda válida</div>');
  assert.equal(mismatch.status, 'parsed');
  assert.equal(mismatch.updatePanels[0].html, '<div>Materia 3.1.099 | estado anterior</div>');
  assert.notDeepEqual(empty.updatePanels, mismatch.updatePanels);
});

test('discards active nodes and returns only inert panels and hidden fields', () => {
  const text = [
    '4|onSubmit|submit|evil|',
    '4|expando|target|evil|',
    '4|scriptBlock|script|evil|',
    '2|updatePanel|panel|ok|',
  ].join('');

  assert.deepEqual(parseDeltaResponse(text), {
    status: 'parsed',
    updatePanels: [{ id: 'panel', html: 'ok' }],
    hiddenFields: [],
  });
});

test('maps error and redirect nodes to fixed outcomes without exposing their content', () => {
  const error = parseDeltaResponse(fixture('delta-error.txt'));
  const redirect = parseDeltaResponse(fixture('delta-redirect.txt'));

  assert.deepEqual(error, { status: 'failed', reason: 'delta_error' });
  assert.deepEqual(redirect, { status: 'failed', reason: 'delta_redirect' });
  assert.doesNotMatch(JSON.stringify(error), /detalle|interno|exponer/);
  assert.doesNotMatch(JSON.stringify(redirect), /example|token|sintético/);
});

test('rejects truncated content with a fixed outcome', () => {
  assert.deepEqual(parseDeltaResponse(fixture('delta-malformed.txt')), {
    status: 'failed',
    reason: 'delta_truncated',
  });
});

test('rejects negative and non-numeric lengths', () => {
  for (const text of ['-1|updatePanel|panel||', 'x|updatePanel|panel|value|']) {
    assert.deepEqual(parseDeltaResponse(text), {
      status: 'failed',
      reason: 'delta_invalid_length',
    });
  }
});

test('rejects missing structural and trailing delimiters', () => {
  for (const text of ['2|updatePanel', '2|updatePanel|panel|ok']) {
    assert.deepEqual(parseDeltaResponse(text), {
      status: 'failed',
      reason: 'delta_missing_delimiter',
    });
  }
});

test('caps the number of parsed nodes before accumulating more data', () => {
  const text = '1|hiddenField|one|a|1|hiddenField|two|b|';

  assert.deepEqual(parseDeltaResponse(text, { maxNodes: 1 }), {
    status: 'failed',
    reason: 'delta_too_many_nodes',
  });
});

test('caps total input characters before parsing or accumulating content', () => {
  const text = '2|updatePanel|panel|ok|';

  assert.deepEqual(parseDeltaResponse(text, { maxChars: text.length - 1 }), {
    status: 'failed',
    reason: 'delta_too_large',
  });
});
