import { getDb } from '../../db/database.js';
import { getJob, listJobsByUser } from '../../db/jobs.repository.js';
import { getUser } from '../../db/users.repository.js';
import { formatJobStatus, formatMateria, jobNotFoundMessage, parseLastOutcome } from '../messages.js';

// Discord rejects an autocomplete choice `name` longer than 100 characters.
const AUTOCOMPLETE_NAME_MAX_LENGTH = 100;

/**
 * Builds the autocomplete label for a job: label, materia code (plus the
 * scraped materia name once a poll has captured it), turno/dias, and
 * current status. Richer than the plain `jobDisplay` confirmation-message
 * format, so duplicate/similar searches (same label, D-07) stay
 * distinguishable in the `/detener`, `/pausar`, `/reanudar` picker.
 *
 * @param {import('zod').infer<typeof import('../../schemas.js').SearchJobSchema>} job
 * @param {import('zod').infer<typeof import('../../schemas.js').UserRecordSchema> | null} [user]
 * @returns {string}
 */
export function buildJobDisplay(job, user = null) {
  const dias = job.filtros.dias.join('/');
  const materia = formatMateria(job, parseLastOutcome(job.lastOutcome));
  const estado = formatJobStatus(job, user);
  const display = `${job.label} · ${materia} · ${job.filtros.turno} ${dias} · ${estado}`;

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

export function getOwnedJobFromInteraction(db, interaction) {
  const rawJobId = interaction.options.getString('busqueda');
  const jobId = Number(rawJobId);
  if (!Number.isInteger(jobId)) {
    return null;
  }

  const job = getJob(db, jobId);
  if (!job || job.discordUserId !== interaction.user.id) {
    return null;
  }

  return job;
}

export async function replyJobNotFound(interaction) {
  await interaction.reply({ content: jobNotFoundMessage(), ephemeral: true });
}
