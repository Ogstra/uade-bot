import { MessageFlags, SlashCommandBuilder } from 'discord.js';
import { getDb } from '../../db/database.js';
import { updateJobStatus } from '../../db/jobs.repository.js';
import { autocompleteAllJobs, getAnyJobFromInteraction } from './job-selection.js';
import { adminJobActionMessage, adminJobNotFoundMessage } from '../messages.js';
import logger from '../../logger.js';

export const adminPausarCommand = {
  adminOnly: true,
  data: new SlashCommandBuilder()
    .setName('admin-pausar')
    .setDescription('[Admin] Pausar la busqueda de cualquier usuario')
    .addStringOption((option) =>
      option
        .setName('busqueda')
        .setDescription('Busqueda a pausar (de cualquier cuenta)')
        .setRequired(true)
        .setAutocomplete(true),
    ),

  autocomplete: autocompleteAllJobs,

  async execute(interaction, { db = getDb() } = {}) {
    const job = getAnyJobFromInteraction(db, interaction);
    if (!job) {
      await interaction.reply({ content: adminJobNotFoundMessage(), flags: MessageFlags.Ephemeral });
      return;
    }

    updateJobStatus(db, job.id, 'paused_by_user');
    logger.warn(
      { event: 'admin_job_paused', jobId: job.id, targetUserId: job.discordUserId, adminUserId: interaction.user.id },
      'Admin paused a search belonging to another account',
    );
    await interaction.reply({ content: adminJobActionMessage('pausada', job), flags: MessageFlags.Ephemeral });
  },
};
