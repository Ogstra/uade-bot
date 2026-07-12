import { SlashCommandBuilder } from 'discord.js';
import { getDb } from '../../db/database.js';
import { updateJobStatus } from '../../db/jobs.repository.js';
import { autocompleteUserJobs, buildJobDisplay, getOwnedJobFromInteraction, replyJobNotFound } from './job-selection.js';

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

  async execute(interaction, { db = getDb() } = {}) {
    const job = getOwnedJobFromInteraction(db, interaction);
    if (!job) {
      await replyJobNotFound(interaction);
      return;
    }

    updateJobStatus(db, job.id, 'active');
    await interaction.reply({ content: `Busqueda reanudada: ${buildJobDisplay(job)}.`, ephemeral: true });
  },
};
