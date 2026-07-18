import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { fileURLToPath } from 'node:url';

import {
  buildMateriaCatalogPayload,
  buildSearchPayload,
  extractReflectedSearchState,
  hasMateriaCheckboxes,
  verifyPostbackMatchesQuery,
} from './webforms.js';

const fixtureUrl = (name) => new URL(`./__fixtures__/webforms/${name}`, import.meta.url);
const readFixture = (name) => readFile(fixtureUrl(name), 'utf8');

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

test('buildSearchPayload selects the unique form containing the Buscar submit', async () => {
  const html = await readFixture('initial-form.html');
  const unrelatedForm = '<form action="/login"><input name="username"><button type="submit">Ingresar</button></form>';
  const result = buildSearchPayload(html.replace('<body>', `<body>${unrelatedForm}`), filtros);

  assert.equal(result.formAction, '/InscripcionClaseBuscar.aspx');
  assert.equal(result.payload.get('ctl00$ContentPlaceHolder1$btnBuscar'), 'Buscar');
});

test('buildSearchPayload accepts materia code and name in the same cell', async () => {
  const html = await readFixture('initial-form.html');
  const combinedCellHtml = html.replace('<td>3.1.050</td><td>Física II</td>', '<td>3.1.050 - Física II</td><td></td>');
  const result = buildSearchPayload(combinedCellHtml, filtros);

  assert.equal(result.materiaNombre, 'Física II');
  assert.equal(result.payload.get('ctl00$ContentPlaceHolder1$ucMateriaInscripcionBuscador$rptMaterias$ctl00$grdMaterias$ctl02$chkSeleccionar'), 'on');
});

test('buildMateriaCatalogPayload posts the seleccionar materias WebForms event without a search submit', async () => {
  const html = (await readFixture('initial-form.html'))
    .replace(
      /<table id="ContentPlaceHolder1_ucMateriaInscripcionBuscador_grdMaterias">[\s\S]*?<\/table>/,
      `<a id="ContentPlaceHolder1_btnSeleccionarMaterias" href="javascript:__doPostBack('ctl00$ContentPlaceHolder1$btnSeleccionarMaterias','')">Seleccionar materias</a>`,
    );
  const result = buildMateriaCatalogPayload(html);

  assert.equal(hasMateriaCheckboxes(html), false);
  assert.equal(result.formAction, '/InscripcionClaseBuscar.aspx');
  assert.equal(result.payload.get('__EVENTTARGET'), 'ctl00$ContentPlaceHolder1$btnSeleccionarMaterias');
  assert.equal(result.payload.get('__EVENTARGUMENT'), '');
  assert.equal(result.payload.get('ctl00$ScriptManager1'), 'ctl00$UpdatePanelContenido|ctl00$ContentPlaceHolder1$btnSeleccionarMaterias');
  assert.equal(result.payload.has('ctl00$ContentPlaceHolder1$btnBuscar'), false);
});

test('buildSearchPayload rejects ambiguous Buscar forms', async () => {
  const html = await readFixture('initial-form.html');
  const duplicate = '<form><input type="submit" name="otherSearch" value="Buscar"></form>';

  assert.throws(
    () => buildSearchPayload(html.replace('<body>', `<body>${duplicate}`), filtros),
    (error) => error?.code === 'WEBFORMS_SUBMIT_AMBIGUOUS',
  );
});

test('materia selection requires an exact code-cell match', async () => {
  const html = await readFixture('initial-form.html');

  for (const collision of ['3.1.0500', '13.1.050']) {
    const candidate = html.replaceAll('3.1.050', collision);
    assert.throws(
      () => buildSearchPayload(candidate, filtros),
      (error) => error?.code === 'WEBFORMS_MATERIA_MISSING',
    );
  }
});

test('mock contract and limits remain stable', async () => {
  const html = await readFixture('initial-form.html');
  const result = buildSearchPayload(html, filtros);

  assert.deepEqual([...new Set(result.payload.keys())].sort(), [...result.requestContract.successfulControlNames].sort());
  assert.equal(result.postbackModeAccepted, 'accepted');
  assert.equal(result.requestContract.method, 'POST');
  assert.equal(result.requestContract.formAction, '/InscripcionClaseBuscar.aspx');
  assert.equal(result.requestContract.contentType, 'application/x-www-form-urlencoded');
  assert.equal(result.requestContract.submitName, 'ctl00$ContentPlaceHolder1$btnBuscar');
  assert.equal(result.requestContract.triggerId, 'ctl00$ContentPlaceHolder1$btnBuscar');
  assert.ok(result.derivedLimits.maxBodyBytes > result.derivedLimits.measuredMaxBodyBytes);
  assert.ok(result.derivedLimits.maxDeltaChars > result.derivedLimits.measuredDeltaChars);
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

test('reflected materia verification rejects prefix and suffix code collisions', async () => {
  const html = await readFixture('postback-found.html');

  for (const collision of ['3.1.0500', '13.1.050']) {
    const reflected = extractReflectedSearchState(html.replace('3.1.050', collision), filtros.materiaCodigo);
    assert.equal(reflected.materiaCodigo, collision);
    assert.equal(verifyPostbackMatchesQuery(reflected, { ...filtros, dias: ['LU', 'MI'] }), false);
  }
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
