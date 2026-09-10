import { MessageFlags } from 'discord.js';
import { getDb } from '../../db/database.js';
import { getJob, listAllJobs, listDistinctJobOwnerIds, listJobsByUser } from '../../db/jobs.repository.js';
import { getUser, listAllUserIds } from '../../db/users.repository.js';
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

/**
 * Resolves a Discord user id to a human-readable name for autocomplete
 * labels. Autocomplete choice names are plain text -- unlike message
 * content, `<@id>` mentions do NOT get resolved to a display name there --
 * so admin pickers need an actual REST fetch to show something recognizable.
 * Falls back to the raw id if the fetch fails (unreachable account, etc.).
 *
 * @param {import('discord.js').Client} client
 * @param {string} discordUserId
 * @returns {Promise<string>}
 */
export async function resolveDisplayName(client, discordUserId) {
  try {
    const user = await client.users.fetch(discordUserId);
    return user.globalName || user.username || discordUserId;
  } catch {
    return discordUserId;
  }
}

/**
 * Admin-only autocomplete: every account's jobs, not just the caller's own
 * (unlike `autocompleteUserJobs`). Each label is prefixed with the job id
 * and the owning account's resolved display name, since duplicate labels
 * across *different* accounts are far more likely than within one account,
 * and the job id alone doesn't tell an admin whose search it is.
 */
export async function autocompleteAllJobs(interaction, { db = getDb() } = {}) {
  const focused = String(interaction.options.getFocused() ?? '').toLowerCase();
  const jobs = listAllJobs(db);
  const uniqueOwnerIds = [...new Set(jobs.map((job) => job.discordUserId))];
  const nameByOwnerId = new Map(
    await Promise.all(
      uniqueOwnerIds.map(async (id) => [id, await resolveDisplayName(interaction.client, id)]),
    ),
  );

  const choices = jobs
    .map((job) => ({
      name: `#${job.id} · ${nameByOwnerId.get(job.discordUserId)} · ${buildJobDisplay(job, getUser(db, job.discordUserId))}`.slice(0, 100),
      value: String(job.id),
    }))
    .filter((choice) => choice.name.toLowerCase().includes(focused))
    .slice(0, 25);

  await interaction.respond(choices);
}

/**
 * Shared by every autocomplete that offers "pick an account" choices:
 * resolves each id to a display name and builds `{name, value}` pairs
 * filtered by the currently-typed text, value is the raw discord user id.
 */
async function buildUserChoices(client, userIds, focused) {
  const entries = await Promise.all(
    userIds.map(async (id) => ({ id, name: await resolveDisplayName(client, id) })),
  );

  return entries
    .map((entry) => ({ name: `${entry.name} (${entry.id})`.slice(0, 100), value: entry.id }))
    .filter((choice) => choice.name.toLowerCase().includes(focused))
    .slice(0, 25);
}

/**
 * Backs the `/admin-estado` `usuario` filter: one choice per account that
 * currently has at least one job.
 */
export async function autocompleteJobOwners(interaction, { db = getDb() } = {}) {
  const focused = String(interaction.options.getFocused() ?? '').toLowerCase();
  const choices = await buildUserChoices(interaction.client, listDistinctJobOwnerIds(db), focused);
  await interaction.respond(choices);
}

/**
 * Backs the `/admin-user-stats` `usuario` option: every registered account,
 * including ones with zero jobs -- unlike `autocompleteJobOwners`, an admin
 * may want a status check on an account that never created a search.
 */
export async function autocompleteAllUsers(interaction, { db = getDb() } = {}) {
  const focused = String(interaction.options.getFocused() ?? '').toLowerCase();
  const choices = await buildUserChoices(interaction.client, listAllUserIds(db), focused);
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

/**
 * Admin-only lookup by job id, no ownership check -- the whole point of
 * `/admin-detener` is acting on jobs the admin doesn't own.
 */
export function getAnyJobFromInteraction(db, interaction) {
  const jobId = Number(interaction.options.getString('busqueda'));
  return Number.isInteger(jobId) ? getJob(db, jobId) : null;
}

export async function replyJobNotFound(interaction) {
  await interaction.reply({ content: jobNotFoundMessage(), flags: MessageFlags.Ephemeral });
}
