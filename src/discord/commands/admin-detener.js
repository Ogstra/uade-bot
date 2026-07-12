import { SlashCommandBuilder } from 'discord.js';
import { getDb } from '../../db/database.js';
import { deleteJob } from '../../db/jobs.repository.js';
import { autocompleteAllJobs, getAnyJobFromInteraction } from './job-selection.js';
import { adminJobNotFoundMessage, adminJobStoppedMessage } from '../messages.js';
import logger from '../../logger.js';

export const adminDetenerCommand = {
  adminOnly: true,
  data: new SlashCommandBuilder()
    .setName('admin-detener')
    .setDescription('[Admin] Detener la busqueda de cualquier usuario')
    .addStringOption((option) =>
      option
        .setName('busqueda')
        .setDescription('Busqueda a detener (de cualquier cuenta)')
        .setRequired(true)
        .setAutocomplete(true),
    ),

  autocomplete: autocompleteAllJobs,

  async execute(interaction, { db = getDb() } = {}) {
    const job = getAnyJobFromInteraction(db, interaction);
    if (!job) {
      await interaction.reply({ content: adminJobNotFoundMessage(), ephemeral: true });
      return;
    }

    deleteJob(db, job.id);
    logger.warn(
      { event: 'admin_job_stopped', jobId: job.id, targetUserId: job.discordUserId, adminUserId: interaction.user.id },
      'Admin stopped a search belonging to another account',
    );
    await interaction.reply({ content: adminJobStoppedMessage(job), ephemeral: true });
  },
};
