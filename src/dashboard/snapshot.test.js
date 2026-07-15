import assert from 'node:assert/strict';
import test from 'node:test';

import { safeOutcomeFromStored } from './snapshot.js';

test('safeOutcomeFromStored maps persisted outcomes to closed Spanish labels', () => {
  assert.deepEqual(
    safeOutcomeFromStored(JSON.stringify({
      outcome: 'found',
      vacancies: [
        { turno: 'Noche', sede: 'Monserrat', horario: '18:30', dias: ['LU'], cupos: 2 },
        { turno: 'Noche', sede: 'Monserrat', horario: '20:00', dias: ['MI'], cupos: 3 },
      ],
    })),
    {
      code: 'found',
      label: 'Vacantes encontradas',
      tone: 'healthy',
      vacancyCount: 2,
      totalCupos: 5,
    },
  );

  assert.deepEqual(safeOutcomeFromStored(JSON.stringify({ outcome: 'no_vacancies' })), {
    code: 'no_vacancies',
    label: 'Sin vacantes',
    tone: 'neutral',
    vacancyCount: null,
    totalCupos: null,
  });
  assert.equal(safeOutcomeFromStored(null).label, 'Sin sondeos todavía');
  assert.equal(safeOutcomeFromStored(JSON.stringify({ outcome: 'search_failed', reason: 'private error' })).label, 'Búsqueda fallida');
  assert.equal(safeOutcomeFromStored(JSON.stringify({ outcome: 'invalid_credentials' })).label, 'Credenciales inválidas');
  assert.equal(safeOutcomeFromStored(JSON.stringify({ outcome: 'rate_limited' })).label, 'Limitada por UADE');
  assert.equal(safeOutcomeFromStored(JSON.stringify({ outcome: 'stale_start_url' })).label, 'Link de inscripción vencido');
  assert.equal(safeOutcomeFromStored(JSON.stringify({ outcome: 'needs_new_start_url' })).label, 'Link de inscripción vencido');
});

test('safeOutcomeFromStored never echoes malformed, unknown, markup, or secret-shaped values', () => {
  const sentinels = [
    '<script>alert(1)</script>',
    'uadePassword=SECRET_PASSWORD',
    'param=SECRET_START_URL',
    'ciphertext=SECRET_CIPHER',
  ];
  const inputs = [
    '{bad json',
    JSON.stringify({ outcome: sentinels[0], reason: sentinels[1], uadeStartUrl: sentinels[2] }),
    JSON.stringify({ outcome: 'found', vacancies: [{ cupos: sentinels[3] }] }),
  ];

  for (const input of inputs) {
    const projected = safeOutcomeFromStored(input);
    assert.deepEqual(projected, {
      code: 'unknown',
      label: 'Resultado no reconocido',
      tone: 'warning',
      vacancyCount: null,
      totalCupos: null,
    });
    const serialized = JSON.stringify(projected);
    for (const sentinel of sentinels) {
      assert.equal(serialized.includes(sentinel), false);
    }
  }
});
