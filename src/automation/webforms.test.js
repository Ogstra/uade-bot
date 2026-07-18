import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { fileURLToPath } from 'node:url';

import {
  buildSearchPayload,
  extractReflectedSearchState,
  verifyPostbackMatchesQuery,
} from './webforms.js';

const fixtureUrl = (name) => new URL(`./__fixtures__/webforms/${name}`, import.meta.url);
const readFixture = (name) => readFile(fixtureUrl(name), 'utf8');
const manifestUrl = new URL('../../.planning/phases/03.2-motor-http-sin-navegador/evidence/webforms-capture-sanitized/manifest.json', import.meta.url);

const filtros = {
  materiaCodigo: '3.1.050',
  ofrecimiento: 'curricular',
  turno: 'mañana',
  dias: ['LU'],
  sedesExcluidas: [],
};

test('buildSearchPayload serializes successful controls using names and values from the form', async () => {
  const html = await readFixture('initial-form.html');
  const result = buildSearchPayload(html, filtros);

  assert.ok(result.payload instanceof URLSearchParams);
  assert.equal(result.formAction, '/InscripcionClaseBuscar.aspx');
  assert.equal(result.materiaNombre, 'Física II');
  assert.equal(result.payload.get('__VIEWSTATE'), 'DUMMY_VIEWSTATE');
  assert.equal(result.payload.get('__EVENTVALIDATION'), 'DUMMY_EVENTVALIDATION');
  assert.equal(result.payload.get('ctl00$ContentPlaceHolder1$optOfrecimiento'), '145');
  assert.equal(result.payload.get('ctl00$ContentPlaceHolder1$cboTurno'), '10152');
  assert.equal(result.payload.get('ctl00$ContentPlaceHolder1$chkLunes'), 'on');
  assert.equal(result.payload.get('ctl00$ContentPlaceHolder1$ucMateriaInscripcionBuscador$rptMaterias$ctl00$grdMaterias$ctl02$chkSeleccionar'), 'on');
  assert.equal(result.payload.get('ctl00$ContentPlaceHolder1$btnBuscar'), 'Buscar');
  assert.equal(result.payload.has('ctl00$ContentPlaceHolder1$btnCancelar'), false);
  assert.equal(result.payload.has('disabledField'), false);
  assert.equal(result.payload.has('uncheckedField'), false);
  assert.equal([...result.payload.keys()].includes(''), false);
});

test('mock contract and limits remain traceable to the approved manifest', async () => {
  const [html, manifestText] = await Promise.all([
    readFixture('initial-form.html'),
    readFile(manifestUrl, 'utf8'),
  ]);
  const manifest = JSON.parse(manifestText);
  const result = buildSearchPayload(html, filtros);

  assert.deepEqual([...new Set(result.payload.keys())].sort(), [...manifest.requestContract.successfulControlNames].sort());
  assert.equal(result.postbackModeAccepted, manifest.postbackModeAccepted);
  assert.deepEqual(result.requestContract, manifest.requestContract);
  assert.deepEqual(result.derivedLimits, manifest.derivedLimits);
  assert.ok(result.derivedLimits.maxBodyBytes > Math.max(...Object.values(manifest.responses).map((item) => item.bytes)));
  assert.ok(result.derivedLimits.maxDeltaChars > manifest.responses.asyncPostback.utf16CodeUnits);
  assert.ok(result.derivedLimits.maxDeltaNodes > result.derivedLimits.measuredDeltaNodes);
});

test('buildSearchPayload fails with fixed codes when required form state is absent', async () => {
  const html = await readFixture('initial-form.html');
  const cases = [
    ['<html></html>', 'WEBFORMS_FORM_MISSING'],
    [html.replace('3.1.050', '3.1.051'), 'WEBFORMS_MATERIA_MISSING'],
    [html.replace('<option value="10152">MAÑANA</option>', ''), 'WEBFORMS_TURNO_MISSING'],
    [html.replace('value="Buscar"', 'value="No buscar"'), 'WEBFORMS_SUBMIT_MISSING'],
  ];

  for (const [candidate, expectedCode] of cases) {
    assert.throws(() => buildSearchPayload(candidate, filtros), (error) => error?.code === expectedCode);
  }
});

test('extractReflectedSearchState reads complete matching state from found and empty responses', async () => {
  for (const name of ['postback-found.html', 'postback-empty.html']) {
    const reflected = extractReflectedSearchState(await readFixture(name), filtros.materiaCodigo);
    assert.deepEqual(reflected, {
      materiaCodigo: '3.1.050',
      materiaNombre: 'Física II',
      ofrecimiento: 'curricular',
      turno: 'MAÑANA',
      dias: ['LU', 'MI'],
    });
  }
});

test('SEARCH-04 verification is fail-closed for every missing or mismatched field', () => {
  const completeFiltros = { ...filtros, dias: ['LU', 'MI'] };
  const matching = {
    materiaCodigo: '3.1.050',
    materiaNombre: 'Física II',
    ofrecimiento: 'curricular',
    turno: 'MAÑANA',
    dias: ['MI', 'LU'],
  };

  assert.equal(verifyPostbackMatchesQuery(matching, completeFiltros), true);
  assert.equal(verifyPostbackMatchesQuery(null, completeFiltros), false);
  for (const field of ['materiaCodigo', 'ofrecimiento', 'turno', 'dias']) {
    assert.equal(verifyPostbackMatchesQuery({ ...matching, [field]: null }, completeFiltros), false);
  }
  assert.equal(verifyPostbackMatchesQuery({ ...matching, materiaCodigo: '3.1.099' }, completeFiltros), false);
  assert.equal(verifyPostbackMatchesQuery({ ...matching, ofrecimiento: 'optativa' }, completeFiltros), false);
  assert.equal(verifyPostbackMatchesQuery({ ...matching, turno: 'TARDE' }, completeFiltros), false);
  assert.equal(verifyPostbackMatchesQuery({ ...matching, dias: ['LU'] }, completeFiltros), false);
});

test('postback-empty is valid only when all reflected fields match', async () => {
  const completeFiltros = { ...filtros, dias: ['LU', 'MI'] };
  const emptyState = extractReflectedSearchState(await readFixture('postback-empty.html'), filtros.materiaCodigo);
  const mismatchState = extractReflectedSearchState(await readFixture('postback-mismatch.html'), filtros.materiaCodigo);

  assert.equal(verifyPostbackMatchesQuery(emptyState, completeFiltros), true);
  assert.equal(verifyPostbackMatchesQuery(mismatchState, completeFiltros), false);
});

test('fixtures contain no live secret or session material', async () => {
  for (const name of ['initial-form.html', 'postback-found.html', 'postback-empty.html', 'postback-mismatch.html']) {
    const html = await readFixture(name);
    assert.doesNotMatch(html, /Authorization\s*:\s*Basic|Set-Cookie|[?&]param=|password|sessionuade/i);
  }
});

test('webforms helper has no environment, Playwright, script execution, or resource-loading dependency', async () => {
  const source = await readFile(new URL('./webforms.js', import.meta.url), 'utf8');
  assert.doesNotMatch(source, /loadEnv|playwright|<script|eval\s*\(|new Function|fetch\s*\(/i);
  assert.equal(fileURLToPath(new URL('./webforms.js', import.meta.url)).endsWith('webforms.js'), true);
});
