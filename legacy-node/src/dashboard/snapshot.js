const OUTCOME_LABELS = Object.freeze({
  no_vacancies: { label: 'Sin vacantes', tone: 'neutral' },
  search_failed: { label: 'Búsqueda fallida', tone: 'failure' },
  invalid_credentials: { label: 'Credenciales inválidas', tone: 'failure' },
  rate_limited: { label: 'Limitada por UADE', tone: 'warning' },
  stale_start_url: { label: 'Link de inscripción vencido', tone: 'warning' },
  needs_new_start_url: { label: 'Link de inscripción vencido', tone: 'warning' },
});

const UNKNOWN_OUTCOME = Object.freeze({
  code: 'unknown',
  label: 'Resultado no reconocido',
  tone: 'warning',
  vacancyCount: null,
  totalCupos: null,
});

const PAUSE_LABELS = Object.freeze({
  needs_credentials: {
    accountLabel: 'Pausada: requiere credenciales',
    breakdownLabel: 'Requiere credenciales',
  },
  needs_new_start_url: {
    accountLabel: 'Pausada: requiere un nuevo link',
    breakdownLabel: 'Requiere un nuevo link',
  },
  rate_limited: {
    accountLabel: 'Pausada temporalmente por límite de UADE',
    breakdownLabel: 'Límite de UADE',
  },
});

const DEFAULT_REPOSITORIES = Object.freeze({
  listAllJobs,
  listPausedAccounts,
  listHistoryForJob,
  getMateriaNombre,
});

function outcomeProjection(code, label, tone, vacancyCount = null, totalCupos = null) {
  return { code, label, tone, vacancyCount, totalCupos };
}

/**
 * Parses a job's stored outcome at the web trust boundary and returns only
 * closed, display-safe fields. Raw reasons and future fields are discarded.
 */
export function safeOutcomeFromStored(serializedOutcome) {
  if (serializedOutcome == null || serializedOutcome === '') {
    return outcomeProjection(null, 'Sin sondeos todavía', 'neutral');
  }

  let parsed;
  try {
    parsed = JSON.parse(serializedOutcome);
  } catch {
    return { ...UNKNOWN_OUTCOME };
  }

  if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
    return { ...UNKNOWN_OUTCOME };
  }

  if (parsed.outcome === 'found' || parsed.outcome === 'vacancies_found') {
    if (
      !Array.isArray(parsed.vacancies)
      || parsed.vacancies.length === 0
      || parsed.vacancies.some((vacancy) => (
        !vacancy
        || typeof vacancy !== 'object'
        || !Number.isInteger(vacancy.cupos)
        || vacancy.cupos < 0
      ))
    ) {
      return { ...UNKNOWN_OUTCOME };
    }

    return outcomeProjection(
      'found',
      'Vacantes encontradas',
      'healthy',
      parsed.vacancies.length,
      parsed.vacancies.reduce((total, vacancy) => total + vacancy.cupos, 0),
    );
  }

  const known = OUTCOME_LABELS[parsed.outcome];
  if (!known) {
    return { ...UNKNOWN_OUTCOME };
  }

  return outcomeProjection(parsed.outcome, known.label, known.tone);
}

function safePauseReason(reason) {
  const known = PAUSE_LABELS[reason];
  if (known) {
    return { code: reason, ...known };
  }
  return {
    code: 'unknown',
    accountLabel: 'Pausada: revisar el estado',
    breakdownLabel: 'Revisar el estado',
  };
}

function accountStatus(pauseReason) {
  if (!pauseReason) {
    return { code: 'active', label: 'Activa', tone: 'healthy' };
  }
  const safePause = safePauseReason(pauseReason);
  return { code: safePause.code, label: safePause.accountLabel, tone: 'warning' };
}

function jobStatus(status, pauseReason) {
  if (pauseReason) {
    return accountStatus(pauseReason);
  }
  if (status === 'paused_by_user') {
    return { code: 'paused_by_user', label: 'Pausada manualmente', tone: 'warning' };
  }
  if (status === 'active') {
    return { code: 'active', label: 'Activa', tone: 'healthy' };
  }
  return { code: 'unknown', label: 'Revisar el estado', tone: 'warning' };
}

function historyOutcome(record) {
  if (record.outcomeCode === 'found') {
    return outcomeProjection(
      'found',
      'Vacantes encontradas',
      'healthy',
      Number.isInteger(record.vacancyCount) && record.vacancyCount >= 0 ? record.vacancyCount : null,
      Number.isInteger(record.totalCupos) && record.totalCupos >= 0 ? record.totalCupos : null,
    );
  }
  const known = OUTCOME_LABELS[record.outcomeCode];
  return known
    ? outcomeProjection(record.outcomeCode, known.label, known.tone)
    : { ...UNKNOWN_OUTCOME };
}

function safeFilters(filtros) {
  const excluded = Array.isArray(filtros?.sedesExcluidas)
    ? filtros.sedesExcluidas.filter((sede) => typeof sede === 'string')
    : [];
  const dias = Array.isArray(filtros?.dias)
    ? filtros.dias.filter((dia) => typeof dia === 'string')
    : [];

  return {
    materiaCodigo: typeof filtros?.materiaCodigo === 'string' ? filtros.materiaCodigo : 'No disponible',
    ofrecimiento: filtros?.ofrecimiento === 'optativa' ? 'Optativa' : 'Curricular',
    turno: typeof filtros?.turno === 'string' ? filtros.turno : 'No disponible',
    dias,
    sedesExcluidas: excluded,
    sedesExcluidasLabel: excluded.length > 0 ? excluded.join(', ') : 'Sin exclusiones',
  };
}

function safeMateriaNombre(value) {
  if (typeof value !== 'string') return null;
  const normalized = value.trim();
  return normalized.length > 0 ? normalized.slice(0, 160) : null;
}

function cachedDisplayName(client, discordUserId) {
  const cached = client?.users?.cache?.get?.(discordUserId);
  const value = cached?.displayName ?? cached?.globalName ?? cached?.username;
  return typeof value === 'string' && value.length > 0
    ? value
    : `Usuario ${discordUserId}`;
}

function cachedGuilds(client) {
  const guildCache = client?.guilds?.cache;
  const guilds = typeof guildCache?.values === 'function' ? [...guildCache.values()] : [];
  return guilds
    .map((guild) => ({
      id: typeof guild?.id === 'string' ? guild.id : null,
      name: safeMateriaNombre(guild?.name) ?? 'Servidor sin nombre',
    }))
    .filter((guild) => guild.id)
    .sort((left, right) => left.name.localeCompare(right.name, 'es') || left.id.localeCompare(right.id));
}

function projectHistory(records) {
  return records
    .slice(0, 10)
    .map((record) => ({
      id: record.id,
      recordedAt: record.recordedAt,
      outcome: historyOutcome(record),
    }))
    .sort((left, right) => right.recordedAt - left.recordedAt || right.id - left.id);
}

/**
 * Builds the single allowlisted dashboard read model used by SSR and JSON.
 * Repository injection is supported for deterministic boundary tests.
 */
export function buildDashboardSnapshot({
  db,
  client,
  now = Date.now,
  repositories = DEFAULT_REPOSITORIES,
}) {
  const generatedAt = typeof now === 'function' ? now() : now;
  const jobs = repositories.listAllJobs(db);
  const pausedUsers = repositories.listPausedAccounts(db);
  const pausedByUserId = new Map(pausedUsers.map((user) => [user.discordUserId, user]));
  const grouped = new Map();
  let lastSuccessfulPollAt = null;

  for (const job of jobs) {
    const pause = pausedByUserId.get(job.discordUserId) ?? null;
    const rawHistory = repositories.listHistoryForJob(db, job.id, { limit: 10 });
    const history = projectHistory(rawHistory);
    for (const item of history) {
      if (
        (item.outcome.code === 'found' || item.outcome.code === 'no_vacancies')
        && (lastSuccessfulPollAt == null || item.recordedAt > lastSuccessfulPollAt)
      ) {
        lastSuccessfulPollAt = item.recordedAt;
      }
    }

    const currentOutcome = safeOutcomeFromStored(job.lastOutcome);
    if (
      (currentOutcome.code === 'found' || currentOutcome.code === 'no_vacancies')
      && Number.isInteger(job.lastPolledAt)
      && (lastSuccessfulPollAt == null || job.lastPolledAt > lastSuccessfulPollAt)
    ) {
      lastSuccessfulPollAt = job.lastPolledAt;
    }

    let account = grouped.get(job.discordUserId);
    if (!account) {
      account = {
        discordUserId: job.discordUserId,
        displayName: cachedDisplayName(client, job.discordUserId),
        status: accountStatus(pause?.pauseReason),
        pauseUntil: Number.isInteger(pause?.pauseUntil) ? pause.pauseUntil : null,
        jobCount: 0,
        lastPolledAt: null,
        jobs: [],
      };
      grouped.set(job.discordUserId, account);
    }

    account.jobCount += 1;
    if (Number.isInteger(job.lastPolledAt) && (account.lastPolledAt == null || job.lastPolledAt > account.lastPolledAt)) {
      account.lastPolledAt = job.lastPolledAt;
    }
    const filters = safeFilters(job.filtros);
    const materiaNombre = safeMateriaNombre(
      repositories.getMateriaNombre?.(db, filters.materiaCodigo),
    );
    account.jobs.push({
      jobId: job.id,
      label: job.label,
      status: jobStatus(job.status, pause?.pauseReason),
      lastPolledAt: Number.isInteger(job.lastPolledAt) ? job.lastPolledAt : null,
      outcome: currentOutcome,
      filters: { ...filters, materiaNombre },
      history,
    });
  }

  const pauseBreakdown = new Map();
  for (const user of pausedUsers) {
    const safePause = safePauseReason(user.pauseReason);
    const current = pauseBreakdown.get(safePause.code) ?? {
      code: safePause.code,
      label: safePause.breakdownLabel,
      count: 0,
    };
    current.count += 1;
    pauseBreakdown.set(safePause.code, current);
  }

  const accounts = [...grouped.values()]
    .map((account) => ({
      ...account,
      jobs: account.jobs.sort((left, right) => left.jobId - right.jobId),
    }))
    .sort((left, right) => left.discordUserId.localeCompare(right.discordUserId));
  const accountIdsWithJobs = new Set(jobs.map((job) => job.discordUserId));
  const activeAccounts = [...accountIdsWithJobs]
    .filter((discordUserId) => !pausedByUserId.has(discordUserId)).length;

  return {
    generatedAt,
    botGuilds: cachedGuilds(client),
    health: {
      activeAccounts,
      pausedAccounts: {
        total: pausedUsers.length,
        breakdown: [...pauseBreakdown.values()].sort((left, right) => left.code.localeCompare(right.code)),
      },
      jobs: {
        total: jobs.length,
        active: jobs.filter((job) => job.status === 'active').length,
        manuallyPaused: jobs.filter((job) => job.status === 'paused_by_user').length,
      },
      lastSuccessfulPollAt,
    },
    accounts,
  };
}
import { listAllJobs } from '../db/jobs.repository.js';
import { getMateriaNombre } from '../db/materias.repository.js';
import { listHistoryForJob } from '../db/poll-history.repository.js';
import { listPausedAccounts } from '../db/users.repository.js';
