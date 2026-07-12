import { SlashCommandBuilder } from 'discord.js';
import { getDb } from '../../db/database.js';
import { deleteJob } from '../../db/jobs.repository.js';
import { autocompleteUserJobs, buildJobDisplay, getOwnedJobFromInteraction, replyJobNotFound } from './job-selection.js';

export const detenerCommand = {
  data: new SlashCommandBuilder()
    .setName('detener')
    .setDescription('Detener una de tus busquedas')
    .addStringOption((option) =>
      option
        .setName('busqueda')
        .setDescription('Busqueda a detener')
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

    deleteJob(db, job.id);
    await interaction.reply({ content: `Busqueda detenida: ${buildJobDisplay(job)}.`, ephemeral: true });
  },
};
