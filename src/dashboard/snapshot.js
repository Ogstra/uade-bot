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
