import { MessageFlags, SlashCommandBuilder } from 'discord.js';
import { getDb } from '../../db/database.js';
import { listAllJobs, listJobsByUser } from '../../db/jobs.repository.js';
import { autocompleteJobOwners } from './job-selection.js';
import { adminJobListMessage } from '../messages.js';

export const adminEstadoCommand = {
  adminOnly: true,
  data: new SlashCommandBuilder()
    .setName('admin-estado')
    .setDescription('[Admin] Ver todas las busquedas de todos los usuarios')
    .addStringOption((option) =>
      option
        .setName('usuario')
        .setDescription('Filtrar por una cuenta especifica (opcional)')
        .setRequired(false)
        .setAutocomplete(true),
    ),

  autocomplete: autocompleteJobOwners,

  async execute(interaction, { db = getDb() } = {}) {
    const filterUserId = interaction.options.getString('usuario');
    const jobs = filterUserId ? listJobsByUser(db, filterUserId) : listAllJobs(db);
    await interaction.reply({ content: adminJobListMessage(jobs, interaction.client), flags: MessageFlags.Ephemeral });
  },
};
