import { ZodError } from 'zod';

export function searchValidationError(err) {
  if (err instanceof ZodError) {
    const invalidMateria = err.issues.some((issue) => issue.path.includes('materiaCodigo'));
    if (invalidMateria) {
      return 'El codigo de materia tiene que tener formato N.N.NNN, por ejemplo 3.1.050.';
    }
  }

  return 'No pude validar esos filtros. Revisa materia, dias, turno y ofrecimiento.';
}

export function formatSedes(sedesExcluidas) {
  return sedesExcluidas.length > 0 ? sedesExcluidas.join(', ') : 'ninguna';
}

export function searchCreatedMessage({ job, filtros, pauseReason, credentialResult, requestedCredentials }) {
  const base =
    `Busqueda creada: ${job.label}\n` +
    `Materia: ${filtros.materiaCodigo}\n` +
    `Turno: ${filtros.turno}\n` +
    `Ofrecimiento: ${filtros.ofrecimiento}\n` +
    `Dias: ${filtros.dias.join(', ')}\n` +
    `Sedes excluidas: ${formatSedes(filtros.sedesExcluidas)}`;
  const pausedSuffix = pauseReason
    ? `\nQuedo creada pausada por el estado de tu cuenta: ${pauseReason}.`
    : '';

  if (!requestedCredentials) {
    return `${base}${pausedSuffix}`;
  }

  const credentialSuffix = credentialResult?.ok
    ? '\nComo no tenias credenciales guardadas, te las pedi por DM y quedaron guardadas.'
    : `\n${credentialResult?.message}`;

  return `${base}${pausedSuffix}${credentialSuffix}`;
}

export function noJobsMessage() {
  return 'No tenes busquedas activas.';
}

export function formatJobStatus(job, user) {
  if (job.status === 'paused_by_user') {
    return `pausada${user?.pauseReason ? ` (${user.pauseReason})` : ''}`;
  }

  if (user?.pauseReason) {
    return `pausada (${user.pauseReason})`;
  }

  return 'activa';
}

export function formatJobStatusBlock(job, user) {
  const lastPoll = job.lastPolledAt ?? 'sin sondeos';
  const lastOutcome = job.lastOutcome ?? 'sin resultado';

  return [
    `**${job.label}**`,
    `Materia: ${job.filtros.materiaCodigo}`,
    `Turno: ${job.filtros.turno}`,
    `Dias: ${job.filtros.dias.join(', ')}`,
    `Sedes excluidas: ${formatSedes(job.filtros.sedesExcluidas)}`,
    `Estado: ${formatJobStatus(job, user)}`,
    `Ultimo sondeo: ${lastPoll}`,
    `Ultimo resultado: ${lastOutcome}`,
  ].join('\n');
}

export function jobDisplay(job) {
  const dias = job.filtros.dias.join('/');
  return `${job.label} - ${job.filtros.turno} - ${dias}`;
}

export function jobNotFoundMessage() {
  return 'No encontre esa busqueda entre tus busquedas activas.';
}

export function jobActionMessage(action, job) {
  return `Busqueda ${action}: ${jobDisplay(job)}.`;
}

export const credentialPrompts = {
  username: 'Mandame tu usuario de UADE.',
  password: 'Mandame tu password de UADE.',
  startUrl: 'Mandame el link de inscripcion de UADE.',
  newUsername: 'Mandame tu nuevo usuario de UADE.',
  newPassword: 'Mandame tu nuevo password de UADE.',
  newStartUrl: 'Mandame el nuevo link de inscripcion de UADE.',
};

export function dmUnavailableMessage() {
  return 'No pude abrirte DM. Habilita mensajes privados del servidor y volve a intentar.';
}

export function credentialsSavedMessage() {
  return 'Credenciales guardadas.';
}

export function credentialsUpdatedMessage() {
  return 'Credenciales actualizadas.';
}

export function credentialOnboardingFailedMessage() {
  return 'No pude completar la carga de credenciales. Volve a intentar con /credenciales.';
}

export function credentialRotationFailedMessage() {
  return 'No pude actualizar tus credenciales. Volve a intentar.';
}

function formatVacancyLines(vacancies) {
  return vacancies
    .map(
      (vacancy) =>
        `Sede: ${vacancy.sede}\nHorario: ${vacancy.horario}\nDias: ${vacancy.dias.join(', ')}\n${vacancy.cupos} cupos`,
    )
    .join('\n\n');
}

export function vacancyNotificationMessage(job, outcome, { channel = false } = {}) {
  const title = channel
    ? `<@${job.discordUserId}> se encontro una vacante para ${job.label}.`
    : `Se encontro una vacante para ${job.label}.`;

  return (
    `${title}\n` +
    `Materia: ${job.filtros.materiaCodigo}\n` +
    `Turno buscado: ${job.filtros.turno}\n\n` +
    formatVacancyLines(outcome.vacancies)
  );
}

export function pauseNotificationMessage(reason) {
  if (reason === 'needs_new_start_url') {
    return 'Pausé tus búsquedas porque el link de inscripción de UADE parece vencido. Usá /credenciales modo:link para actualizarlo.';
  }

  return 'Pausé tus búsquedas porque tus credenciales de UADE parecen vencidas. Usá /credenciales para actualizarlas.';
}

export function genericInteractionErrorMessage() {
  return 'No pude procesar ese comando. Proba de nuevo en unos minutos.';
}
