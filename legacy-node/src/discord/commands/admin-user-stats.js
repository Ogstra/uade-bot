import { MessageFlags, SlashCommandBuilder } from 'discord.js';
import { getDb } from '../../db/database.js';
import { listJobsByUser } from '../../db/jobs.repository.js';
import { getUser } from '../../db/users.repository.js';
import { countCommandUsageByCommandForUser, countCommandUsageForUser } from '../../db/command-log.repository.js';
import { autocompleteAllUsers, resolveDisplayName } from './job-selection.js';
import { adminUserNotFoundMessage, adminUserStatsMessage } from '../messages.js';

export const adminUserStatsCommand = {
  adminOnly: true,
  data: new SlashCommandBuilder()
    .setName('admin-user-stats')
    .setDescription('[Admin] Ver estadisticas de una cuenta especifica')
    .addStringOption((option) =>
      option
        .setName('usuario')
        .setDescription('Cuenta a consultar')
        .setRequired(true)
        .setAutocomplete(true),
    ),

  autocomplete: autocompleteAllUsers,

  async execute(interaction, { db = getDb() } = {}) {
    const discordUserId = interaction.options.getString('usuario');
    const user = getUser(db, discordUserId);
    if (!user) {
      await interaction.reply({ content: adminUserNotFoundMessage(), flags: MessageFlags.Ephemeral });
      return;
    }

    const displayName = await resolveDisplayName(interaction.client, discordUserId);
    const jobs = listJobsByUser(db, discordUserId);

    await interaction.reply({
      content: adminUserStatsMessage({
        displayName,
        discordUserId,
        jobs,
        pauseReason: user.pauseReason,
        totalCommandUsage: countCommandUsageForUser(db, discordUserId),
        topCommands: countCommandUsageByCommandForUser(db, discordUserId, { limit: 6 }),
        client: interaction.client,
      }),
      flags: MessageFlags.Ephemeral,
    });
  },
};
