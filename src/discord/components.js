import { ActionRowBuilder, ButtonBuilder, ButtonStyle } from 'discord.js';
import { getDb } from '../db/database.js';
import { deleteJob, updateJobStatus } from '../db/jobs.repository.js';
import { getOwnedJob } from './commands/job-selection.js';
import { jobActionMessage, jobNotFoundMessage } from './messages.js';
import logger from '../logger.js';

const DETENER_BUTTON_PREFIX = 'detener_job:';
const TOGGLE_PAUSE_BUTTON_PREFIX = 'toggle_pause_job:';

// Discord rejects a button `label` longer than 80 characters.
const BUTTON_LABEL_MAX_LENGTH = 80;

function truncateLabel(label) {
  return label.length > BUTTON_LABEL_MAX_LENGTH ? `${label.slice(0, BUTTON_LABEL_MAX_LENGTH - 1)}…` : label;
}

function parsePrefixedJobId(prefix, customId) {
  if (typeof customId !== 'string' || !customId.startsWith(prefix)) {
    return null;
  }

  const jobId = Number(customId.slice(prefix.length));
  return Number.isInteger(jobId) ? jobId : null;
}

/**
 * Attaches a "Detener busqueda" quick-action button to a vacancy
 * notification (DM or channel mention), per NOTIF-01's request to stop a
 * search right from the found-vacancy message.
 *
 * @param {number} jobId
 * @returns {import('discord.js').ActionRowBuilder}
 */
export function buildVacancyActionRow(jobId) {
  const button = new ButtonBuilder()
    .setCustomId(`${DETENER_BUTTON_PREFIX}${jobId}`)
    .setLabel('Detener busqueda')
    .setStyle(ButtonStyle.Danger);

  return new ActionRowBuilder().addComponents(button);
}

export function isDetenerButton(interaction) {
  return interaction.isButton?.() === true && parsePrefixedJobId(DETENER_BUTTON_PREFIX, interaction.customId) !== null;
}

export function isTogglePauseButton(interaction) {
  return (
    interaction.isButton?.() === true &&
    parsePrefixedJobId(TOGGLE_PAUSE_BUTTON_PREFIX, interaction.customId) !== null
  );
}

/**
 * Builds the Pausar-or-Reanudar (toggle, by current status) + Detener
 * button pair for one job, each labeled with the job's own identity so
 * they stay distinguishable once several rows are on screen at once.
 */
function buildJobButtons(job) {
  const toggleLabel = job.status === 'paused_by_user' ? `Reanudar ${job.label}` : `Pausar ${job.label}`;

  const toggleButton = new ButtonBuilder()
    .setCustomId(`${TOGGLE_PAUSE_BUTTON_PREFIX}${job.id}`)
    .setLabel(truncateLabel(toggleLabel))
    .setStyle(ButtonStyle.Secondary);

  const detenerButton = new ButtonBuilder()
    .setCustomId(`${DETENER_BUTTON_PREFIX}${job.id}`)
    .setLabel(truncateLabel(`Detener ${job.label}`))
    .setStyle(ButtonStyle.Danger);

  return [toggleButton, detenerButton];
}

/**
 * Builds one action row per pair of jobs (2 buttons/job, 2 jobs/row) for
 * `/estado`. Bounded by `MAX_ACTIVE_SEARCHES_PER_USER` (job-selection.js)
 * so this never exceeds Discord's 5-row / 25-button-per-message limit.
 *
 * @param {{ id: number, label: string, status: string }[]} jobs
 * @returns {import('discord.js').ActionRowBuilder[]}
 */
export function buildEstadoActionRows(jobs) {
  const rows = [];
  for (let index = 0; index < jobs.length; index += 2) {
    const pairButtons = jobs.slice(index, index + 2).flatMap(buildJobButtons);
    rows.push(new ActionRowBuilder().addComponents(pairButtons));
  }
  return rows;
}

/**
 * Handles a "Detener busqueda" button click. The button is visible to
 * anyone who can see the vacancy message (DM recipient, or anyone in the
 * authorized channel for the mention copy) or the /estado listing (only
 * the caller, since /estado is always ephemeral), so ownership is
 * re-checked here exactly like `/detener` — a click from someone other
 * than the search's own `discordUserId` is treated as not-found, never as
 * a cross-user delete.
 */
export async function handleDetenerButton(interaction, { db = getDb() } = {}) {
  const jobId = parsePrefixedJobId(DETENER_BUTTON_PREFIX, interaction.customId);
  const job = getOwnedJob(db, jobId, interaction.user.id);

  if (!job) {
    await interaction.reply({ content: jobNotFoundMessage(), ephemeral: true });
    return;
  }

  deleteJob(db, job.id);
  await interaction.reply({ content: jobActionMessage('detenida', job), ephemeral: true });
}

/**
 * Handles the /estado Pausar-or-Reanudar toggle button, flipping the job's
 * status and — same as `/reanudar` — enqueuing an immediate poll when the
 * result is a resume, so the next `/estado` reflects a fresh result
 * instead of the stale outcome from before the pause.
 */
export async function handleTogglePauseButton(interaction, { db = getDb(), onJobResumed } = {}) {
  const jobId = parsePrefixedJobId(TOGGLE_PAUSE_BUTTON_PREFIX, interaction.customId);
  const job = getOwnedJob(db, jobId, interaction.user.id);

  if (!job) {
    await interaction.reply({ content: jobNotFoundMessage(), ephemeral: true });
    return;
  }

  const nextStatus = job.status === 'paused_by_user' ? 'active' : 'paused_by_user';
  const updatedJob = updateJobStatus(db, job.id, nextStatus);
  const action = nextStatus === 'active' ? 'reanudada' : 'pausada';
  await interaction.reply({ content: jobActionMessage(action, job), ephemeral: true });

  if (nextStatus === 'active' && onJobResumed) {
    try {
      await Promise.resolve(onJobResumed(updatedJob));
    } catch (err) {
      logger.error(
        { event: 'immediate_poll_enqueue_failed', jobId: updatedJob.id, message: err.message },
        'Immediate poll enqueue failed',
      );
    }
  }
}
