import { ActionRowBuilder, ButtonBuilder, ButtonStyle } from 'discord.js';
import { getDb } from '../db/database.js';
import { deleteJob } from '../db/jobs.repository.js';
import { getOwnedJob } from './commands/job-selection.js';
import { jobActionMessage, jobNotFoundMessage } from './messages.js';

const DETENER_BUTTON_PREFIX = 'detener_job:';

function parseDetenerJobId(customId) {
  if (typeof customId !== 'string' || !customId.startsWith(DETENER_BUTTON_PREFIX)) {
    return null;
  }

  const jobId = Number(customId.slice(DETENER_BUTTON_PREFIX.length));
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
  return interaction.isButton?.() === true && parseDetenerJobId(interaction.customId) !== null;
}

/**
 * Handles a "Detener busqueda" button click. The button is visible to
 * anyone who can see the vacancy message (DM recipient, or anyone in the
 * authorized channel for the mention copy), so ownership is re-checked
 * here exactly like `/detener` — a click from someone other than the
 * search's own `discordUserId` is treated as not-found, never as a
 * cross-user delete.
 */
export async function handleDetenerButton(interaction, { db = getDb() } = {}) {
  const jobId = parseDetenerJobId(interaction.customId);
  const job = getOwnedJob(db, jobId, interaction.user.id);

  if (!job) {
    await interaction.reply({ content: jobNotFoundMessage(), ephemeral: true });
    return;
  }

  deleteJob(db, job.id);
  await interaction.reply({ content: jobActionMessage('detenida', job), ephemeral: true });
}
