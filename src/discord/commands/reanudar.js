import { SlashCommandBuilder } from 'discord.js';
import { getDb } from '../../db/database.js';
import { updateJobStatus } from '../../db/jobs.repository.js';
import { autocompleteUserJobs, getOwnedJobFromInteraction, replyJobNotFound } from './job-selection.js';
import { jobActionMessage } from '../messages.js';

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

  async execute(interaction, { db = getDb(), ephemeralReplies = false } = {}) {
    const job = getOwnedJobFromInteraction(db, interaction);
    if (!job) {
      await replyJobNotFound(interaction);
      return;
    }

    updateJobStatus(db, job.id, 'active');
    await interaction.reply({ content: jobActionMessage('reanudada', job), ephemeral: ephemeralReplies });
  },
};
