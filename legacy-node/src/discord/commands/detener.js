import { SlashCommandBuilder } from 'discord.js';
import { getDb } from '../../db/database.js';
import { deleteJob } from '../../db/jobs.repository.js';
import { autocompleteUserJobs, getOwnedJobFromInteraction, replyJobNotFound } from './job-selection.js';
import { jobActionMessage } from '../messages.js';
import { ephemeralFlags } from '../reply-flags.js';

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

  async execute(interaction, { db = getDb(), ephemeralReplies = false } = {}) {
    const job = getOwnedJobFromInteraction(db, interaction);
    if (!job) {
      await replyJobNotFound(interaction);
      return;
    }

    deleteJob(db, job.id);
    await interaction.reply({ content: jobActionMessage('detenida', job), ...ephemeralFlags(ephemeralReplies) });
  },
};
