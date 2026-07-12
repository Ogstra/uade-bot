import { ActionRowBuilder, ButtonBuilder, ButtonStyle } from 'discord.js';
import { getDb } from '../db/database.js';
import { deleteJob } from '../db/jobs.repository.js';
import { getOwnedJob } from './commands/job-selection.js';
import { jobActionMessage, jobNotFoundMessage } from './messages.js';
import logger from '../logger.js';

const DETENER_BUTTON_PREFIX = 'detener_job:';

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

/**
 * Handles a "Detener busqueda" button click. The button is visible to
 * anyone who can see the vacancy message (DM recipient, or anyone in the
 * authorized channel for the mention copy), so ownership is re-checked
 * here exactly like `/detener` — a click from someone other than the
 * search's own `discordUserId` is treated as not-found, never as a
 * cross-user delete.
 *
 * When the click happens somewhere other than the search's own channel
 * (typically: stopping it from the DM copy of a vacancy notification),
 * the confirmation is also echoed to `job.channelId` so anyone watching
 * the original channel sees the search was stopped, not just the DM.
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

  if (job.channelId && job.channelId !== interaction.channelId) {
    try {
      const channel = await interaction.client.channels.fetch(job.channelId);
      await channel.send(jobActionMessage('detenida', job));
    } catch (err) {
      logger.error(
        { event: 'detener_channel_echo_failed', jobId: job.id, channelId: job.channelId, message: err.message },
        'Detener channel echo failed',
      );
    }
  }
}
