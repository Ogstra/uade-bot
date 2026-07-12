import { getDb } from '../../db/database.js';
import { getJob, listJobsByUser } from '../../db/jobs.repository.js';
import { getUser } from '../../db/users.repository.js';
import { formatJobIdentity, formatJobStatus, jobNotFoundMessage, parseLastOutcome } from '../messages.js';

// Discord rejects an autocomplete choice `name` longer than 100 characters.
const AUTOCOMPLETE_NAME_MAX_LENGTH = 100;

// Caps how many active/paused searches a single account can hold at once.
// Chosen so /estado can attach a Pausar-or-Reanudar + Detener button pair
// per search (2 buttons/job, 2 jobs/row) and still fit Discord's hard limit
// of 5 action rows / 25 buttons per message, with headroom to spare.
export const MAX_ACTIVE_SEARCHES_PER_USER = 10;

/**
 * Builds the autocomplete label for a job: full search identity (etiqueta
 * only when one was set, so the materia code isn't shown twice — D-06/D-07),
 * turno/dias, and current status. Richer than the plain `jobDisplay`
 * confirmation-message format, so duplicate/similar searches stay
 * distinguishable in the `/detener`, `/pausar`, `/reanudar` picker.
 *
 * @param {import('zod').infer<typeof import('../../schemas.js').SearchJobSchema>} job
 * @param {import('zod').infer<typeof import('../../schemas.js').UserRecordSchema> | null} [user]
 * @returns {string}
 */
export function buildJobDisplay(job, user = null) {
  const dias = job.filtros.dias.join('/');
  const identity = formatJobIdentity(job, parseLastOutcome(job.lastOutcome));
  const estado = formatJobStatus(job, user);
  const display = `${identity} · ${job.filtros.turno} ${dias} · ${estado}`;

  return display.length > AUTOCOMPLETE_NAME_MAX_LENGTH
    ? `${display.slice(0, AUTOCOMPLETE_NAME_MAX_LENGTH - 1)}…`
    : display;
}

export async function autocompleteUserJobs(interaction, { db = getDb() } = {}) {
  const focused = String(interaction.options.getFocused() ?? '').toLowerCase();
  const jobs = listJobsByUser(db, interaction.user.id);
  const user = getUser(db, interaction.user.id);
  const choices = jobs
    .map((job) => ({ name: buildJobDisplay(job, user), value: String(job.id) }))
    .filter((choice) => choice.name.toLowerCase().includes(focused))
    .slice(0, 25);

  await interaction.respond(choices);
}

export function getOwnedJob(db, jobId, discordUserId) {
  if (!Number.isInteger(jobId)) {
    return null;
  }

  const job = getJob(db, jobId);
  if (!job || job.discordUserId !== discordUserId) {
    return null;
  }

  return job;
}

export function getOwnedJobFromInteraction(db, interaction) {
  return getOwnedJob(db, Number(interaction.options.getString('busqueda')), interaction.user.id);
}

export async function replyJobNotFound(interaction) {
  await interaction.reply({ content: jobNotFoundMessage(), ephemeral: true });
}
