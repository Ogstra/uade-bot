import assert from 'node:assert/strict';
import { test } from 'node:test';
import {
  credentialOnboardingFailedMessage,
  credentialPrompts,
  credentialsSavedMessage,
  credentialsUpdatedMessage,
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

  assert.match(text, /Busqueda creada: Fisica II/);
  assert.match(text, /Materia: 3\.1\.050/);
  assert.match(text, /Turno: Noche/);
  assert.match(text, /Sedes excluidas: Monserrat/);
  assert.match(text, /Quedo creada pausada/);
  assertNoSecrets(text);
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
