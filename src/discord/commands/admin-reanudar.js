import { SlashCommandBuilder } from 'discord.js';
import { getDb } from '../../db/database.js';
import { updateJobStatus } from '../../db/jobs.repository.js';
import { autocompleteAllJobs, getAnyJobFromInteraction } from './job-selection.js';
import { adminJobActionMessage, adminJobNotFoundMessage } from '../messages.js';
import logger from '../../logger.js';

export const adminReanudarCommand = {
  adminOnly: true,
  data: new SlashCommandBuilder()
    .setName('admin-reanudar')
    .setDescription('[Admin] Reanudar la busqueda de cualquier usuario')
    .addStringOption((option) =>
      option
        .setName('busqueda')
        .setDescription('Busqueda a reanudar (de cualquier cuenta)')
        .setRequired(true)
        .setAutocomplete(true),
    ),

  autocomplete: autocompleteAllJobs,

  async execute(interaction, { db = getDb(), onJobResumed } = {}) {
    const job = getAnyJobFromInteraction(db, interaction);
    if (!job) {
      await interaction.reply({ content: adminJobNotFoundMessage(), ephemeral: true });
      return;
    }

    const resumedJob = updateJobStatus(db, job.id, 'active');
    logger.warn(
      { event: 'admin_job_resumed', jobId: job.id, targetUserId: job.discordUserId, adminUserId: interaction.user.id },
      'Admin resumed a search belonging to another account',
    );
    await interaction.reply({ content: adminJobActionMessage('reanudada', job), ephemeral: true });

    if (onJobResumed) {
      try {
        await Promise.resolve(onJobResumed(resumedJob));
      } catch (err) {
        logger.error(
          { event: 'immediate_poll_enqueue_failed', jobId: resumedJob.id, message: err.message },
          'Immediate poll enqueue failed',
        );
      }
    }
  },
};
