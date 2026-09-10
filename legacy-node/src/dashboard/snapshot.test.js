import assert from 'node:assert/strict';
import test from 'node:test';

import { buildDashboardSnapshot, safeOutcomeFromStored } from './snapshot.js';

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

test('buildDashboardSnapshot groups jobs by account and derives health from safe history', () => {
  const jobs = [
    {
      id: 20,
      discordUserId: 'user-b',
      label: 'Álgebra',
      status: 'active',
      lastPolledAt: 400,
      lastOutcome: JSON.stringify({ outcome: 'search_failed', reason: 'SECRET_FAILURE' }),
      filtros: {
        materiaCodigo: '1.1.020', ofrecimiento: 'curricular', turno: 'Noche', dias: ['LU'], sedesExcluidas: [],
      },
      channelId: 'SECRET_CHANNEL', guildId: 'SECRET_GUILD', lastNotifiedState: 'SECRET_NOTIFY',
    },
    {
      id: 10,
      discordUserId: 'user-a',
      label: 'Análisis',
      status: 'paused_by_user',
      lastPolledAt: 300,
      lastOutcome: JSON.stringify({ outcome: 'no_vacancies', uadePassword: 'SECRET_PASSWORD' }),
      filtros: {
        materiaCodigo: '1.1.010', ofrecimiento: 'optativa', turno: 'Mañana', dias: ['MA', 'JU'], sedesExcluidas: ['Costa Argentina'],
      },
      ciphertext: 'SECRET_CIPHER', uadeStartUrl: 'param=SECRET_PARAM',
    },
  ];
  const histories = new Map([
    [20, [
      { id: 3, jobId: 20, recordedAt: 400, outcomeCode: 'search_failed', vacancyCount: null, totalCupos: null },
      { id: 2, jobId: 20, recordedAt: 200, outcomeCode: 'found', vacancyCount: 1, totalCupos: 4 },
    ]],
    [10, [
      { id: 1, jobId: 10, recordedAt: 300, outcomeCode: 'no_vacancies', vacancyCount: null, totalCupos: null },
    ]],
  ]);
  let fetchCalls = 0;
  const client = {
    users: {
      cache: new Map([['user-a', { displayName: 'Ana' }], ['user-b', { username: 'Beto' }]]),
      fetch() {
        fetchCalls += 1;
        throw new Error('REST must not be called');
      },
    },
    guilds: {
      cache: new Map([
        ['guild-b', { id: 'guild-b', name: 'OG2' }],
        ['guild-a', { id: 'guild-a', name: 'Servidor <script>bad()</script>' }],
      ]),
    },
  };
  const repositories = {
    listAllJobs: () => jobs,
    listPausedAccounts: () => [{ discordUserId: 'user-a', pauseReason: 'needs_credentials', pauseUntil: null }],
    getMateriaNombre: (_db, codigo) => ({
      '1.1.010': 'Análisis Matemático',
      '1.1.020': 'Álgebra Lineal',
    })[codigo] ?? null,
    listHistoryForJob: (_db, jobId, options) => {
      assert.deepEqual(options, { limit: 10 });
      return histories.get(jobId) ?? [];
    },
  };

  const snapshot = buildDashboardSnapshot({ db: {}, client, now: () => 500, repositories });

  assert.equal(snapshot.generatedAt, 500);
  assert.deepEqual(snapshot.botGuilds, [
    { id: 'guild-b', name: 'OG2' },
    { id: 'guild-a', name: 'Servidor <script>bad()</script>' },
  ]);
  assert.deepEqual(snapshot.health, {
    activeAccounts: 1,
    pausedAccounts: {
      total: 1,
      breakdown: [{ code: 'needs_credentials', label: 'Requiere credenciales', count: 1 }],
    },
    jobs: { total: 2, active: 1, manuallyPaused: 1 },
    lastSuccessfulPollAt: 300,
  });
  assert.deepEqual(snapshot.accounts.map((account) => account.discordUserId), ['user-a', 'user-b']);
  assert.equal(snapshot.accounts[0].displayName, 'Ana');
  assert.equal(snapshot.accounts[1].displayName, 'Beto');
  assert.equal(snapshot.accounts[0].status.label, 'Pausada: requiere credenciales');
  assert.equal(snapshot.accounts[0].jobs[0].status.label, 'Pausada: requiere credenciales');
  assert.deepEqual(snapshot.accounts[0].jobs[0].filters, {
    materiaCodigo: '1.1.010',
    materiaNombre: 'Análisis Matemático',
    ofrecimiento: 'Optativa',
    turno: 'Mañana',
    dias: ['MA', 'JU'],
    sedesExcluidas: ['Costa Argentina'],
    sedesExcluidasLabel: 'Costa Argentina',
  });
  assert.equal(snapshot.accounts[1].jobs[0].filters.sedesExcluidasLabel, 'Sin exclusiones');
  assert.equal(snapshot.accounts[1].jobs[0].filters.materiaNombre, 'Álgebra Lineal');
  assert.deepEqual(snapshot.accounts[1].jobs[0].history.map((item) => item.id), [3, 2]);
  assert.equal(snapshot.accounts[1].jobs[0].history[1].outcome.label, 'Vacantes encontradas');
  assert.equal(fetchCalls, 0);

  const serialized = JSON.stringify(snapshot);
  for (const forbidden of [
    'SECRET_FAILURE', 'SECRET_PASSWORD', 'SECRET_CHANNEL', 'SECRET_GUILD',
    'SECRET_NOTIFY', 'SECRET_CIPHER', 'SECRET_PARAM', 'channelId', 'guildId',
    'lastOutcome', 'lastNotifiedState', 'ciphertext', 'uadeStartUrl',
  ]) {
    assert.equal(serialized.includes(forbidden), false, `snapshot leaked ${forbidden}`);
  }
});

test('buildDashboardSnapshot returns a stable empty state and safe unknown pause copy', () => {
  const repositories = {
    listAllJobs: () => [],
    listPausedAccounts: () => [{ discordUserId: 'unused-user', pauseReason: '<script>bad</script>' }],
    listHistoryForJob: () => { throw new Error('no jobs means no history reads'); },
  };

  assert.deepEqual(buildDashboardSnapshot({ db: {}, client: null, now: 700, repositories }), {
    generatedAt: 700,
    botGuilds: [],
    health: {
      activeAccounts: 0,
      pausedAccounts: {
        total: 1,
        breakdown: [{ code: 'unknown', label: 'Revisar el estado', count: 1 }],
      },
      jobs: { total: 0, active: 0, manuallyPaused: 0 },
      lastSuccessfulPollAt: null,
    },
    accounts: [],
  });
});
