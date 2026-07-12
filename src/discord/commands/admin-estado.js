import { SlashCommandBuilder } from 'discord.js';
import { getDb } from '../../db/database.js';
import { listAllJobs } from '../../db/jobs.repository.js';
import { adminJobListMessage } from '../messages.js';

export const adminEstadoCommand = {
  adminOnly: true,
  data: new SlashCommandBuilder()
    .setName('admin-estado')
    .setDescription('[Admin] Ver todas las busquedas de todos los usuarios'),

  async execute(interaction, { db = getDb() } = {}) {
    const jobs = listAllJobs(db);
    await interaction.reply({ content: adminJobListMessage(jobs), ephemeral: true });
  },
};
