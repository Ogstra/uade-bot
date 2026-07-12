import { ZodError } from 'zod';

export function searchValidationError(err) {
  if (err instanceof ZodError) {
    const invalidMateria = err.issues.some((issue) => issue.path.includes('materiaCodigo'));
    if (invalidMateria) {
      return 'El codigo de materia tiene que tener formato `N.N.NNN`, por ejemplo `3.1.050`.';
    }
  }

  return 'No pude validar esos filtros. Revisa **materia**, **dias**, **turno** y **ofrecimiento**.';
}

export function tooManySearchesMessage(max) {
  return `Ya tenes ${max} busquedas activas, el maximo por cuenta. Detene alguna con \`/detener\` antes de crear otra.`;
}

export function formatSedes(sedesExcluidas) {
  return sedesExcluidas.length > 0 ? sedesExcluidas.join(', ') : 'ninguna';
}

export function parseLastOutcome(lastOutcome) {
  if (!lastOutcome) {
    return null;
  }

  try {
    return JSON.parse(lastOutcome);
  } catch {
    return { outcome: lastOutcome };
  }
}

export function formatMateria(job, outcome = null) {
  return outcome?.materiaNombre
    ? `${job.filtros.materiaCodigo} - ${outcome.materiaNombre}`
    : job.filtros.materiaCodigo;
}

/**
 * Full search identity for messages that otherwise only show `job.label`:
 * `codigo` or `codigo - nombre` (once a poll has captured it), prefixed
 * with the custom `etiqueta` when one was given (label !== materiaCodigo).
 * Exported for reuse in autocomplete labels (job-selection.js), which
 * would otherwise show the materia code twice when no etiqueta was set.
 */
export function formatJobIdentity(job, outcome = null) {
  const materia = formatMateria(job, outcome);
  return job.label === job.filtros.materiaCodigo ? materia : `${job.label} - ${materia}`;
}

function optionalSedesLine(sedesExcluidas) {
  return sedesExcluidas.length > 0 ? [`**Sedes excluidas:** ${formatSedes(sedesExcluidas)}`] : [];
}

export function searchCreatedMessage({ job, filtros, pauseReason, credentialResult, requestedCredentials, materiaNombre }) {
  const base = [
    `**Busqueda creada:** ${job.label}`,
    `**Materia:** \`${filtros.materiaCodigo}\`${materiaNombre ? ` - ${materiaNombre}` : ''}`,
    `**Turno:** ${filtros.turno}`,
    `**Ofrecimiento:** ${filtros.ofrecimiento}`,
    `**Dias:** ${filtros.dias.join(', ')}`,
    ...optionalSedesLine(filtros.sedesExcluidas),
  ].join('\n');
  const pausedSuffix = pauseReason
    ? `\nQuedo creada pausada por el estado de tu cuenta: _${pauseReason}_.`
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
  return '_No tenes busquedas activas._';
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

export function formatLastPoll(lastPolledAt) {
  if (!lastPolledAt) {
    return 'sin sondeos';
  }

  return new Intl.DateTimeFormat('es-AR', {
    dateStyle: 'short',
    timeStyle: 'medium',
  }).format(new Date(lastPolledAt));
}

export function formatLastOutcome(lastOutcome) {
  const outcome = parseLastOutcome(lastOutcome);
  if (!outcome) {
    return 'sin resultado';
  }

  if (outcome.outcome === 'found') {
    const vacancies = Array.isArray(outcome.vacancies) ? outcome.vacancies : [];
    const total = vacancies.reduce((sum, vacancy) => sum + Number(vacancy.cupos ?? 0), 0);
    return vacancies.length === 1
      ? `vacante encontrada (${total} cupos)`
      : `vacantes encontradas (${vacancies.length} cursos, ${total} cupos)`;
  }

  if (outcome.outcome === 'no_vacancies') {
    return 'sin vacantes';
  }

  if (outcome.outcome === 'invalid_credentials') {
    return 'credenciales invalidas';
  }

  if (outcome.outcome === 'search_failed') {
    return outcome.reason ? `fallo la busqueda: ${outcome.reason}` : 'fallo la busqueda';
  }

  return String(outcome.outcome ?? lastOutcome);
}

/**
 * Where a job lives: the server it was created in (resolved to a name via
 * the bot's own guild cache -- no extra fetch needed, the bot is already a
 * member of every guild any of its jobs could belong to) plus a channel
 * mention, which Discord renders as a clickable `#channel-name` and
 * implicitly confirms which server too. Falls back to "DM" for a job
 * created outside any guild, since a DM channel id doesn't render as a
 * usable mention.
 *
 * @param {{ channelId: string | null, guildId: string | null }} job
 * @param {import('discord.js').Client} [client]
 */
export function formatJobLocation(job, client) {
  if (!job.guildId) {
    return job.channelId ? 'DM' : 'sin canal';
  }

  const guildName = client?.guilds?.cache?.get(job.guildId)?.name ?? job.guildId;
  return job.channelId ? `${guildName} · <#${job.channelId}>` : guildName;
}

export function formatJobStatusBlock(job, user, client) {
  const lastOutcome = parseLastOutcome(job.lastOutcome);

  return [
    `**${job.label}**`,
    `**Materia:** ${formatMateria(job, lastOutcome)}`,
    `**Turno:** ${job.filtros.turno}`,
    `**Dias:** ${job.filtros.dias.join(', ')}`,
    ...optionalSedesLine(job.filtros.sedesExcluidas),
    `**Server/Canal:** ${formatJobLocation(job, client)}`,
    `**Estado:** ${formatJobStatus(job, user)}`,
    `**Ultimo sondeo:** ${formatLastPoll(job.lastPolledAt)}`,
    `**Ultimo resultado:** ${formatLastOutcome(job.lastOutcome)}`,
  ].join('\n');
}

export function jobDisplay(job) {
  const { turno, ofrecimiento, dias, sedesExcluidas } = job.filtros;
  const identity = formatJobIdentity(job, parseLastOutcome(job.lastOutcome));
  const sedesSuffix = sedesExcluidas.length > 0 ? ` (sin ${formatSedes(sedesExcluidas)})` : '';
  return `${identity} - ${turno} - ${ofrecimiento} - ${dias.join('/')}${sedesSuffix}`;
}

export function jobNotFoundMessage() {
  return 'No encontre esa busqueda entre tus busquedas activas.';
}

export function jobActionMessage(action, job) {
  return `**Busqueda ${action}:** ${jobDisplay(job)}.`;
}

export const credentialPrompts = {
  username: 'Mandame tu **usuario** de UADE.',
  password: 'Mandame tu **password** de UADE.',
  // Only shown as a FALLBACK now (Fase 3.1) -- the bot tries to obtener el
  // link de inscripción solo, automatizando el login de Microsoft/Azure AD.
  // Este prompt aparece únicamente si eso falla (típicamente: la cuenta
  // tiene verificación en dos pasos y el bot no puede completarla).
  startUrl:
    'No pude conseguir tu link de inscripción automáticamente (puede que tu cuenta tenga verificación en dos pasos, ' +
    'u otro paso extra que el bot no puede completar solo). Mandámelo vos: el **link de inscripcion** de UADE ' +
    '(el que te lleva directo al buscador, ya logueado). Algo asi: `https://inscripcionespia.uade.edu.ar/...?param=xxxxx`.',
  newUsername: 'Mandame tu **nuevo usuario** de UADE.',
  newPassword: 'Mandame tu **nuevo password** de UADE.',
  newStartUrl:
    'Mandame el **nuevo link de inscripcion** de UADE. Algo asi: `https://inscripcionespia.uade.edu.ar/...?param=xxxxx`.',
};

export function dmUnavailableMessage() {
  return 'No pude abrirte DM. **Habilita mensajes privados** del servidor y volve a intentar.';
}

export function credentialsSavedMessage() {
  return '**Credenciales guardadas.**';
}

export function credentialsUpdatedMessage() {
  return '**Credenciales actualizadas.**';
}

export function credentialOnboardingFailedMessage() {
  return 'No pude completar la carga de credenciales. Volve a intentar con `/credenciales`.';
}

export function credentialRotationFailedMessage() {
  return 'No pude actualizar tus credenciales. Volve a intentar.';
}

function formatVacancyLines(vacancies) {
  return vacancies
    .map(
      (vacancy) =>
        `**Sede:** ${vacancy.sede}\n**Horario:** ${vacancy.horario}\n**Dias:** ${vacancy.dias.join(', ')}\n**${vacancy.cupos} cupos**`,
    )
    .join('\n\n');
}

export function vacancyNotificationMessage(job, outcome, { channel = false } = {}) {
  const identity = formatJobIdentity(job, outcome);
  const title = channel
    ? `<@${job.discordUserId}> se encontro una vacante para **${identity}**.`
    : `Se encontro una vacante para **${identity}**.`;

  return (
    `${title}\n` +
    `**Turno:** ${job.filtros.turno}\n\n` +
    formatVacancyLines(outcome.vacancies)
  );
}

export function pauseNotificationMessage(reason) {
  if (reason === 'needs_new_start_url') {
    return 'Pausé tus búsquedas porque el link de inscripción de UADE parece vencido. Usá `/credenciales modo:link` para actualizarlo.';
  }

  return 'Pausé tus búsquedas porque tus credenciales de UADE parecen vencidas. Usá `/credenciales` para actualizarlas.';
}

export function genericInteractionErrorMessage() {
  return 'No pude procesar ese comando. Proba de nuevo en unos minutos.';
}

export function notAuthorizedMessage() {
  return 'Ese comando es solo para administradores del servidor.';
}

export function adminNoJobsMessage() {
  return '_No hay busquedas activas ni pausadas en ninguna cuenta._';
}

/**
 * One compact line per job for `/admin-estado` -- unlike `formatJobStatusBlock`
 * (multi-line, one caller's own searches), this lists every account's jobs
 * in one message, so it has to stay terse to fit Discord's 2000-char limit.
 */
export function formatAdminJobLine(job, client) {
  const identity = formatJobIdentity(job, parseLastOutcome(job.lastOutcome));
  const estado = job.status === 'paused_by_user' ? 'pausada' : 'activa';
  return `**#${job.id}** <@${job.discordUserId}> · ${identity} · ${job.filtros.turno} ${job.filtros.dias.join('/')} · ${estado} · ${formatJobLocation(job, client)} · ${formatLastOutcome(job.lastOutcome)}`;
}

/**
 * @param {import('zod').infer<typeof import('../schemas.js').SearchJobSchema>[]} jobs
 * @param {import('discord.js').Client} [client]
 */
export function adminJobListMessage(jobs, client) {
  if (jobs.length === 0) {
    return adminNoJobsMessage();
  }

  const lines = jobs.map((job) => formatAdminJobLine(job, client));
  const joined = lines.join('\n');

  // Discord message content cap is 2000 chars -- truncate defensively
  // rather than let a large friends-group roster fail to send at all.
  if (joined.length <= 1900) {
    return joined;
  }

  let truncated = '';
  let shown = 0;
  for (const line of lines) {
    if (truncated.length + line.length + 1 > 1850) {
      break;
    }
    truncated += (truncated ? '\n' : '') + line;
    shown += 1;
  }
  return `${truncated}\n_...y ${jobs.length - shown} mas (no entran en un solo mensaje)._`;
}

export function adminJobNotFoundMessage() {
  return 'No encontre ninguna busqueda (de nadie) con ese id.';
}

/**
 * Shared confirmation for every admin job-action command (`/admin-detener`,
 * `/admin-pausar`, `/admin-reanudar`) -- always names the owning account
 * (`<@discordUserId>`) since these act on searches the admin doesn't own.
 */
export function adminJobActionMessage(action, job) {
  return `**Busqueda ${action} (admin):** #${job.id} de <@${job.discordUserId}> — ${jobDisplay(job)}.`;
}

export function adminUserNotFoundMessage() {
  return 'Esa cuenta no esta registrada (nunca uso el bot).';
}

/**
 * Per-account breakdown for `/admin-user-stats` -- unlike `adminStatsMessage`
 * (bot-wide totals), this scopes everything to one account: its jobs (via
 * `formatAdminJobLine`, so each line still shows the owner mention/id even
 * though it's redundant here, for visual consistency with `/admin-estado`),
 * pause state, and command usage.
 */
export function adminUserStatsMessage({ displayName, discordUserId, jobs, pauseReason, totalCommandUsage, topCommands, client }) {
  const activeJobs = jobs.filter((job) => job.status === 'active').length;
  const pausedJobs = jobs.filter((job) => job.status === 'paused_by_user').length;

  const lines = [
    `**Estadisticas de ${displayName}** (<@${discordUserId}>)`,
    `**Estado de la cuenta:** ${pauseReason ? `pausada (${pauseReason})` : 'activa'}`,
    `**Busquedas activas:** ${activeJobs}`,
    `**Busquedas pausadas (por usuario):** ${pausedJobs}`,
    `**Comandos ejecutados (total):** ${totalCommandUsage}`,
  ];

  if (topCommands.length > 0) {
    lines.push('**Comandos mas usados:**');
    lines.push(topCommands.map((c) => `  - \`/${c.commandName}\`: ${c.count}`).join('\n'));
  }

  if (jobs.length > 0) {
    lines.push('**Busquedas:**');
    lines.push(jobs.map((job) => formatAdminJobLine(job, client)).join('\n'));
  }

  return lines.join('\n');
}

export function adminStatsMessage({
  totalUsers,
  totalActiveJobs,
  totalPausedJobs,
  pausedAccounts,
  materiasCached,
  totalCommandUsage,
  topCommands,
}) {
  const lines = [
    '**Estadisticas del bot**',
    `**Usuarios registrados:** ${totalUsers}`,
    `**Busquedas activas:** ${totalActiveJobs}`,
    `**Busquedas pausadas (por usuario):** ${totalPausedJobs}`,
    `**Cuentas pausadas (credenciales/link/rate limit):** ${pausedAccounts.length}`,
  ];

  if (pausedAccounts.length > 0) {
    lines.push(
      pausedAccounts
        .map((user) => `  - <@${user.discordUserId}>: _${user.pauseReason}_`)
        .join('\n'),
    );
  }

  lines.push(`**Materias en cache:** ${materiasCached}`);
  lines.push(`**Comandos ejecutados (total):** ${totalCommandUsage}`);

  if (topCommands.length > 0) {
    lines.push('**Comandos mas usados:**');
    lines.push(topCommands.map((c) => `  - \`/${c.commandName}\`: ${c.count}`).join('\n'));
  }

  return lines.join('\n');
}
