import { SlashCommandBuilder } from 'discord.js';
import { getDb } from '../../db/database.js';
import { updateJobStatus } from '../../db/jobs.repository.js';
import { autocompleteUserJobs, buildJobDisplay, getOwnedJobFromInteraction, replyJobNotFound } from './job-selection.js';

export const pausarCommand = {
  data: new SlashCommandBuilder()
    .setName('pausar')
    .setDescription('Pausar una de tus busquedas')
    .addStringOption((option) =>
      option
        .setName('busqueda')
        .setDescription('Busqueda a pausar')
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

    updateJobStatus(db, job.id, 'paused_by_user');
    await interaction.reply({ content: `Busqueda pausada: ${buildJobDisplay(job)}.`, ephemeral: true });
  },
};
