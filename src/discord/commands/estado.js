import { MessageFlags, SlashCommandBuilder } from 'discord.js';
import { getDb } from '../../db/database.js';
import { getUser } from '../../db/users.repository.js';
import { listJobsByUser } from '../../db/jobs.repository.js';
import { formatJobStatusBlock, noJobsMessage } from '../messages.js';

export const estadoCommand = {
  data: new SlashCommandBuilder()
    .setName('estado')
    .setDescription('Ver tus busquedas activas o pausadas'),

  async execute(interaction, { db = getDb() } = {}) {
    const jobs = listJobsByUser(db, interaction.user.id);
    if (jobs.length === 0) {
      await interaction.reply({ content: noJobsMessage(), flags: MessageFlags.Ephemeral });
      return;
    }

    const user = getUser(db, interaction.user.id);
    await interaction.reply({
      content: jobs.map((job) => formatJobStatusBlock(job, user, interaction.client)).join('\n\n'),
      flags: MessageFlags.Ephemeral,
    });
  },
};
