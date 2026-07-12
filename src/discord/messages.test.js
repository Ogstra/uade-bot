import assert from 'node:assert/strict';
import { test } from 'node:test';
import {
  credentialOnboardingFailedMessage,
  credentialPrompts,
  credentialsSavedMessage,
  credentialsUpdatedMessage,
  formatJobStatusBlock,
  jobActionMessage,
  pauseNotificationMessage,
  searchCreatedMessage,
  vacancyNotificationMessage,
} from './messages.js';

const FILTROS = {
  materiaCodigo: '3.1.050',
  ofrecimiento: 'curricular',
  turno: 'Noche',
  dias: ['LU', 'MI'],
  sedesExcluidas: ['Monserrat'],
};

const JOB = {
  id: 1,
  discordUserId: 'user-1',
  label: 'Fisica II',
  filtros: FILTROS,
};

const SECRET_VALUES = {
  username: 'alumno.secreto',
  password: 'password-super-secreta',
  startUrl: 'https://inscripcionespia.uade.edu.ar/?param=secreto',
};

function assertNoSecrets(text) {
  for (const secret of Object.values(SECRET_VALUES)) {
    assert.equal(text.includes(secret), false);
  }
}

test('searchCreatedMessage includes filters and paused-account context without secrets', () => {
  const text = searchCreatedMessage({
    job: JOB,
    filtros: FILTROS,
    pauseReason: 'needs_credentials',
    requestedCredentials: true,
    credentialResult: { ok: true, message: credentialsSavedMessage(), values: SECRET_VALUES },
  });

  assert.match(text, /\*\*Busqueda creada:\*\* Fisica II/);
  assert.match(text, /\*\*Materia:\*\* `3\.1\.050`/);
  assert.match(text, /\*\*Turno:\*\* Noche/);
  assert.match(text, /\*\*Sedes excluidas:\*\* Monserrat/);
  assert.match(text, /Quedo creada pausada/);
  assertNoSecrets(text);
});

test('searchCreatedMessage omits sedes excluidas when none were excluded', () => {
  const text = searchCreatedMessage({
    job: { ...JOB, filtros: { ...FILTROS, sedesExcluidas: [] } },
    filtros: { ...FILTROS, sedesExcluidas: [] },
    pauseReason: null,
    requestedCredentials: false,
  });

  assert.doesNotMatch(text, /Sedes excluidas/);
});

test('credential copy confirms save or update without echoing values', () => {
  const allText = [
    credentialPrompts.username,
    credentialPrompts.password,
    credentialPrompts.startUrl,
    credentialsSavedMessage(),
    credentialsUpdatedMessage(),
    credentialOnboardingFailedMessage(),
  ].join('\n');

  assert.match(allText, /UADE/);
  assertNoSecrets(allText);
});

test('vacancy and pause notification copy is Spanish and contains vacancy fields only', () => {
  const vacancyText = vacancyNotificationMessage(
    JOB,
    {
      outcome: 'found',
      vacancies: [
        {
          turno: 'Noche',
          sede: 'Monserrat',
          horario: '18:30 22:00',
          dias: ['LU', 'MI'],
          cupos: 3,
        },
      ],
    },
    { channel: true },
  );
  const pauseText = pauseNotificationMessage('needs_new_start_url');

  assert.match(vacancyText, /<@user-1>/);
  assert.match(vacancyText, /Fisica II/);
  assert.match(vacancyText, /Monserrat/);
  assert.match(vacancyText, /18:30 22:00/);
  assert.match(vacancyText, /LU, MI/);
  assert.match(vacancyText, /3 cupos/);
  assert.match(pauseText, /\/credenciales/);
  assertNoSecrets(`${vacancyText}\n${pauseText}`);
});

test('formatJobStatusBlock renders poll state without raw epoch or JSON', () => {
  const text = formatJobStatusBlock(
    {
      ...JOB,
      status: 'active',
      lastPolledAt: 1783837905670,
      lastOutcome: JSON.stringify({
        outcome: 'found',
        materiaNombre: 'Fisica II',
        vacancies: [
          {
            turno: 'MAÑANA',
            sede: 'MONSERRAT',
            horario: '07:45 11:45',
            dias: ['MI'],
            cupos: 18,
          },
        ],
      }),
    },
    null,
  );

  assert.match(text, /\*\*Ultimo sondeo:\*\* /);
  assert.match(text, /\*\*Materia:\*\* 3\.1\.050 - Fisica II/);
  assert.match(text, /vacante encontrada \(18 cupos\)/);
  assert.doesNotMatch(text, /1783837905670/);
  assert.doesNotMatch(text, /\{"outcome"/);
});

test('vacancyNotificationMessage renders extracted materia name when present', () => {
  const text = vacancyNotificationMessage(JOB, {
    outcome: 'found',
    materiaNombre: 'Fisica II',
    vacancies: [
      {
        turno: 'Noche',
        sede: 'Monserrat',
        horario: '18:30 22:00',
        dias: ['LU'],
        cupos: 1,
      },
    ],
  });

  assert.match(text, /Se encontro una vacante para \*\*Fisica II - 3\.1\.050 - Fisica II\*\*\./);
});

test('vacancyNotificationMessage omits the etiqueta prefix when label is the bare materia code', () => {
  const jobWithoutEtiqueta = { ...JOB, label: JOB.filtros.materiaCodigo };
  const text = vacancyNotificationMessage(jobWithoutEtiqueta, {
    outcome: 'found',
    materiaNombre: 'Fisica II',
    vacancies: [
      { turno: 'Noche', sede: 'Monserrat', horario: '18:30 22:00', dias: ['LU'], cupos: 1 },
    ],
  });

  assert.match(text, /Se encontro una vacante para \*\*3\.1\.050 - Fisica II\*\*\./);
});

test('jobActionMessage includes materia code and scraped name, not just the label', () => {
  const jobWithOutcome = {
    ...JOB,
    lastOutcome: JSON.stringify({ outcome: 'no_vacancies', materiaNombre: 'Fisica II' }),
  };

  const text = jobActionMessage('pausada', jobWithOutcome);

  assert.match(
    text,
    /\*\*Busqueda pausada:\*\* Fisica II - 3\.1\.050 - Fisica II - Noche - curricular - LU\/MI \(sin Monserrat\)\./,
  );
});

test('jobActionMessage omits the etiqueta prefix when label is the bare materia code', () => {
  const jobWithoutEtiqueta = { ...JOB, label: JOB.filtros.materiaCodigo, lastOutcome: null };

  const text = jobActionMessage('detenida', jobWithoutEtiqueta);

  assert.match(text, /\*\*Busqueda detenida:\*\* 3\.1\.050 - Noche - curricular - LU\/MI \(sin Monserrat\)\./);
});

test('jobActionMessage omits the sedes-excluidas suffix when none were excluded', () => {
  const jobWithoutSedes = {
    ...JOB,
    filtros: { ...JOB.filtros, sedesExcluidas: [] },
    lastOutcome: null,
  };

  const text = jobActionMessage('reanudada', jobWithoutSedes);

  assert.doesNotMatch(text, /sin /);
  assert.match(text, /\*\*Busqueda reanudada:\*\* Fisica II - 3\.1\.050 - Noche - curricular - LU\/MI\./);
});

test('credentialPrompts.startUrl and newStartUrl include a link example', () => {
  assert.match(credentialPrompts.startUrl, /inscripcionespia\.uade\.edu\.ar/);
  assert.match(credentialPrompts.newStartUrl, /inscripcionespia\.uade\.edu\.ar/);
});
