import { MessageFlags, SlashCommandBuilder } from 'discord.js';
import { getDb } from '../../db/database.js';
import { listAllJobs } from '../../db/jobs.repository.js';
import { countUsers, listPausedAccounts } from '../../db/users.repository.js';
import { countMaterias } from '../../db/materias.repository.js';
import { countCommandUsageByCommand, countTotalCommandUsage } from '../../db/command-log.repository.js';
import { adminStatsMessage } from '../messages.js';

export const adminStatsCommand = {
  adminOnly: true,
  data: new SlashCommandBuilder()
    .setName('admin-stats')
    .setDescription('[Admin] Estadisticas generales del bot'),

  async execute(interaction, { db = getDb() } = {}) {
    const jobs = listAllJobs(db);

    await interaction.reply({
      content: adminStatsMessage({
        totalUsers: countUsers(db),
        totalActiveJobs: jobs.filter((job) => job.status === 'active').length,
        totalPausedJobs: jobs.filter((job) => job.status === 'paused_by_user').length,
        pausedAccounts: listPausedAccounts(db),
        materiasCached: countMaterias(db),
        totalCommandUsage: countTotalCommandUsage(db),
        topCommands: countCommandUsageByCommand(db, { limit: 6 }),
      }),
      flags: MessageFlags.Ephemeral,
    });
  },
};
