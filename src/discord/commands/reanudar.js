import { SlashCommandBuilder } from 'discord.js';
import { getDb } from '../../db/database.js';
import { updateJobStatus } from '../../db/jobs.repository.js';
import { autocompleteUserJobs, getOwnedJobFromInteraction, replyJobNotFound } from './job-selection.js';
import { jobActionMessage } from '../messages.js';
import { ephemeralFlags } from '../reply-flags.js';
import logger from '../../logger.js';

export const reanudarCommand = {
  data: new SlashCommandBuilder()
    .setName('reanudar')
    .setDescription('Reanudar una de tus busquedas pausadas')
    .addStringOption((option) =>
      option
        .setName('busqueda')
        .setDescription('Busqueda a reanudar')
        .setRequired(true)
        .setAutocomplete(true),
    ),

  autocomplete: autocompleteUserJobs,

  async execute(interaction, { db = getDb(), ephemeralReplies = false, onJobResumed } = {}) {
    const job = getOwnedJobFromInteraction(db, interaction);
    if (!job) {
      await replyJobNotFound(interaction);
      return;
    }

    const resumedJob = updateJobStatus(db, job.id, 'active');
    await interaction.reply({ content: jobActionMessage('reanudada', job), ...ephemeralFlags(ephemeralReplies) });

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
