import { load } from 'cheerio/slim';

const OFRECIMIENTO_VALUES = Object.freeze({
  curricular: '145',
  optativa: '146',
});

const DIA_CONTROL_SUFFIXES = Object.freeze({
  LU: 'chkLunes',
  MA: 'chkMartes',
  MI: 'chkMiercoles',
  JU: 'chkJueves',
  VI: 'chkViernes',
  SA: 'chkSabado',
});

const REQUEST_CONTRACT = Object.freeze({
  method: 'POST',
  formAction: '/InscripcionClaseBuscar.aspx',
  contentType: 'application/x-www-form-urlencoded',
  headerNames: [
    'accept',
    'cache-control',
    'content-type',
    'cookie',
    'origin',
    'referer',
    'sec-ch-ua',
    'sec-ch-ua-mobile',
    'sec-ch-ua-platform',
    'user-agent',
    'x-microsoftajax',
    'x-requested-with',
  ],
  cookieNames: [
    'ASP.NET_SessionId',
    'TS014a8dbe',
    '_ga',
    '_ga_J9FT1Y0XD5',
    '_gat',
    '_gid',
    'sessionuade',
  ],
  successfulControlNames: [
    'ctl00$ScriptManager1',
    '__EVENTTARGET',
    '__EVENTARGUMENT',
    '__LASTFOCUS',
    '__VIEWSTATE',
    '__VIEWSTATEGENERATOR',
    '__VIEWSTATEENCRYPTED',
    '__EVENTVALIDATION',
    'ctl00$txtAlumnoLegajo',
    'ctl00$txtAlumnoNombre',
    'ctl00$ContentPlaceHolder1$txtCicloLectivo',
    'ctl00$ContentPlaceHolder1$cboPeriodo',
    'ctl00$ContentPlaceHolder1$cboCarrera',
    'ctl00$ContentPlaceHolder1$optOfrecimiento',
    'ctl00$ContentPlaceHolder1$cboTurno',
    'ctl00$ContentPlaceHolder1$chkLunes',
    'ctl00$ContentPlaceHolder1$ucInscripcionClaseDetalle$txtPlan',
    'ctl00$ContentPlaceHolder1$ucInscripcionClaseDetalle$txtPeriodo',
    'ctl00$ContentPlaceHolder1$ucInscripcionClaseDetalle$txtSesion',
    'ctl00$ContentPlaceHolder1$ucInscripcionClaseDetalle$txtNroClase',
    'ctl00$ContentPlaceHolder1$ucInscripcionClaseDetalle$txtCodigo',
    'ctl00$ContentPlaceHolder1$ucInscripcionClaseDetalle$txtNombre',
    'ctl00$ContentPlaceHolder1$ucInscripcionClaseDetalle$txtDocente',
    'ctl00$ContentPlaceHolder1$ucInscripcionClaseDetalle$txtIdioma',
    'ctl00$ContentPlaceHolder1$ucInscripcionClaseDetalle$txtAulaPpal',
    'ctl00$ContentPlaceHolder1$ucInscripcionClaseDetalle$txtFechaInicio',
    'ctl00$ContentPlaceHolder1$ucInscripcionClaseDetalle$txtFechaFin',
    'ctl00$ContentPlaceHolder1$ucInscripcionClaseDetalle$txtHoraInicio',
    'ctl00$ContentPlaceHolder1$ucInscripcionClaseDetalle$txtHoraFin',
    'ctl00$ContentPlaceHolder1$ucInscripcionClaseDetalle$txtCantidadVacantes',
    'ctl00$ContentPlaceHolder1$ucInscripcionClaseDetalle$txtFechaActa',
    'ctl00$ContentPlaceHolder1$ucMateriaInscripcionBuscador$rptMaterias$ctl00$grdMaterias$ctl02$chkSeleccionar',
    '__ASYNCPOST',
    'ctl00$ContentPlaceHolder1$btnBuscar',
  ],
  submitName: 'ctl00$ContentPlaceHolder1$btnBuscar',
  scriptManagerName: 'ctl00$ScriptManager1',
  eventTarget: null,
  panelIds: ['ctl00$UpdatePanelContenido'],
  triggerId: 'ctl00$ContentPlaceHolder1$btnBuscar',
});

const DERIVED_LIMITS = Object.freeze({
  source: 'live response bodies measured before in-memory sanitization',
  margin: {
    strategy: 'ceil(measured * 1.25)',
    ratio: 0.25,
  },
  measuredMaxBodyBytes: 87340,
  measuredDeltaChars: 80154,
  measuredDeltaNodes: 17,
  maxBodyBytes: 109175,
  maxDeltaChars: 100193,
  maxDeltaNodes: 22,
});

function webformsError(code) {
  const error = new Error(code);
  error.code = code;
  return error;
}

function normalizeText(value) {
  return String(value ?? '').replace(/\s+/g, ' ').trim();
}

function optionMatchesLabel(optionText, label) {
  return normalizeText(optionText).localeCompare(normalizeText(label), 'es', { sensitivity: 'base' }) === 0;
}

function extractMateriaCode(cell) {
  return cell.match(/(?:^|[^\d.])(\d+\.\d+\.\d+)(?![\d.])/)?.[1] ?? null;
}

function materiaNameFromCodeCell(cell, materiaCodigo) {
  const remainder = normalizeText(cell.replace(materiaCodigo, '').replace(/^[\s:-]+/, ''));
  return remainder || null;
}

function extractMateriaFromRow($, row) {
  const cells = $(row).find('td').map((_, cell) => normalizeText($(cell).text())).get();
  const codeCellIndex = cells.findIndex((cell) => extractMateriaCode(cell) !== null);
  if (codeCellIndex === -1) {
    return { materiaCodigo: null, materiaNombre: null };
  }
  const materiaCodigo = extractMateriaCode(cells[codeCellIndex]);

  return {
    materiaCodigo,
    materiaNombre: materiaNameFromCodeCell(cells[codeCellIndex], materiaCodigo)
      ?? cells.slice(codeCellIndex + 1).find(Boolean)
      ?? null,
  };
}

function findSearchForm($) {
  const forms = $('form');
  if (forms.length === 0) {
    throw webformsError('WEBFORMS_FORM_MISSING');
  }

  const matchingSubmits = forms
    .find('input[type="submit"], input[type="image"], button[type="submit"], button:not([type])')
    .filter((_, element) => optionMatchesLabel($(element).attr('value') ?? $(element).text(), 'Buscar'));
  if (matchingSubmits.length === 0) {
    throw webformsError('WEBFORMS_SUBMIT_MISSING');
  }
  if (matchingSubmits.length !== 1 || !matchingSubmits.first().attr('name')) {
    throw webformsError('WEBFORMS_SUBMIT_AMBIGUOUS');
  }

  return { form: matchingSubmits.first().closest('form'), chosenSubmit: matchingSubmits.first() };
}

function selectBySemanticSuffix($, form, suffix) {
  return form.find(`select[id$="${suffix}"], select[name$="$${suffix}"]`).first();
}

function controlBySemanticSuffix(form, suffix) {
  return form.find(`input[id$="${suffix}"], input[name$="$${suffix}"]`).first();
}

function serializeSuccessfulControls($, form, chosenSubmit) {
  const payload = new URLSearchParams();

  form.find('input, select, textarea, button').each((_, element) => {
    const control = $(element);
    const name = control.attr('name');
    if (!name || control.is(':disabled')) {
      return;
    }

    const tag = element.tagName.toLowerCase();
    if (tag === 'select') {
      control.find('option:selected').each((__, option) => payload.append(name, $(option).attr('value') ?? $(option).text()));
      return;
    }
    if (tag === 'textarea') {
      payload.append(name, control.text());
      return;
    }

    const type = (control.attr('type') ?? (tag === 'button' ? 'submit' : 'text')).toLowerCase();
    if (type === 'checkbox' || type === 'radio') {
      if (control.is(':checked')) {
        payload.append(name, control.attr('value') ?? 'on');
      }
      return;
    }
    if (type === 'submit' || type === 'image') {
      if (element === chosenSubmit[0]) {
        payload.append(name, control.attr('value') ?? normalizeText(control.text()));
      }
      return;
    }
    if (['button', 'reset', 'file'].includes(type)) {
      return;
    }

    payload.append(name, control.attr('value') ?? '');
  });

  return payload;
}

export function buildSearchPayload(html, filtros) {
  const $ = load(String(html ?? ''));
  const { form, chosenSubmit } = findSearchForm($);

  const materiaCheckboxes = form.find('input[type="checkbox"][id*="chkSeleccionar"]');
  const materiaCheckbox = materiaCheckboxes
    .filter((_, element) => extractMateriaFromRow($, $(element).closest('tr')).materiaCodigo === filtros.materiaCodigo)
    .first();
  if (materiaCheckbox.length === 0) {
    throw webformsError('WEBFORMS_MATERIA_MISSING');
  }
  materiaCheckboxes.prop('checked', false);
  materiaCheckbox.prop('checked', true);
  const { materiaNombre } = extractMateriaFromRow($, materiaCheckbox.closest('tr'));

  const ofrecimientoValue = OFRECIMIENTO_VALUES[filtros.ofrecimiento];
  const ofrecimientoRadios = form.find('input[type="radio"]').filter((_, element) =>
    Object.values(OFRECIMIENTO_VALUES).includes($(element).attr('value')),
  );
  const ofrecimientoRadio = ofrecimientoRadios.filter((_, element) => $(element).attr('value') === ofrecimientoValue).first();
  if (ofrecimientoRadio.length === 0) {
    throw webformsError('WEBFORMS_OFRECIMIENTO_MISSING');
  }
  ofrecimientoRadios.prop('checked', false);
  ofrecimientoRadio.prop('checked', true);

  const turnoSelect = selectBySemanticSuffix($, form, 'cboTurno');
  const turnoOption = turnoSelect.find('option').filter((_, option) => optionMatchesLabel($(option).text(), filtros.turno)).first();
  if (turnoSelect.length === 0 || turnoOption.length === 0) {
    throw webformsError('WEBFORMS_TURNO_MISSING');
  }
  turnoSelect.find('option').prop('selected', false);
  turnoOption.prop('selected', true);

  for (const [dia, suffix] of Object.entries(DIA_CONTROL_SUFFIXES)) {
    const control = controlBySemanticSuffix(form, suffix);
    control.prop('checked', filtros.dias.includes(dia));
  }

  return {
    payload: serializeSuccessfulControls($, form, chosenSubmit),
    formAction: form.attr('action') ?? '',
    materiaNombre,
    postbackModeAccepted: 'accepted',
    requestContract: structuredClone(REQUEST_CONTRACT),
    derivedLimits: structuredClone(DERIVED_LIMITS),
  };
}

export function extractReflectedSearchState(html, expectedMateriaCodigo) {
  const $ = load(String(html ?? ''));
  const form = $('form').first();
  if (form.length === 0) {
    return null;
  }

  const checkedMateria = form.find('input[type="checkbox"][id*="chkSeleccionar"]:checked').first();
  const materia = checkedMateria.length > 0
    ? extractMateriaFromRow($, checkedMateria.closest('tr'))
    : { materiaCodigo: null, materiaNombre: null };

  const checkedOfrecimiento = form.find('input[type="radio"]:checked').filter((_, element) =>
    Object.values(OFRECIMIENTO_VALUES).includes($(element).attr('value')),
  ).first();
  const ofrecimiento = Object.entries(OFRECIMIENTO_VALUES)
    .find(([, value]) => value === checkedOfrecimiento.attr('value'))?.[0] ?? null;

  const turnoSelect = form.find('select#turno, select[id$="_cboTurno"], select[name$="$cboTurno"]').first();
  const turno = normalizeText(turnoSelect.find('option:selected').first().text()) || null;

  const dias = [];
  for (const [dia, suffix] of Object.entries(DIA_CONTROL_SUFFIXES)) {
    if (controlBySemanticSuffix(form, suffix).is(':checked')) {
      dias.push(dia);
    }
  }

  return { ...materia, ofrecimiento, turno, dias };
}

export function verifyPostbackMatchesQuery(reflectedState, filtros) {
  if (!reflectedState || reflectedState.materiaCodigo !== filtros.materiaCodigo) {
    return false;
  }
  if (reflectedState.ofrecimiento !== filtros.ofrecimiento) {
    return false;
  }
  if (typeof reflectedState.turno !== 'string' || !optionMatchesLabel(reflectedState.turno, filtros.turno)) {
    return false;
  }
  if (!Array.isArray(reflectedState.dias) || !Array.isArray(filtros.dias)) {
    return false;
  }

  const reflectedDias = [...reflectedState.dias].sort();
  const submittedDias = [...filtros.dias].sort();
  return reflectedDias.length === submittedDias.length
    && reflectedDias.every((dia, index) => dia === submittedDias[index]);
}
