import { SlashCommandBuilder } from 'discord.js';
import { getDb } from '../../db/database.js';
import { getUser } from '../../db/users.repository.js';
import { listJobsByUser } from '../../db/jobs.repository.js';

function formatStatus(job, user) {
  if (job.status === 'paused_by_user') {
    return `pausada${user?.pauseReason ? ` (${user.pauseReason})` : ''}`;
  }

  if (user?.pauseReason) {
    return `pausada (${user.pauseReason})`;
  }

  return 'activa';
}

function formatJob(job, user) {
  const sedes = job.filtros.sedesExcluidas.length > 0 ? job.filtros.sedesExcluidas.join(', ') : 'ninguna';
  const lastPoll = job.lastPolledAt ?? 'sin sondeos';
  const lastOutcome = job.lastOutcome ?? 'sin resultado';

  return [
    `**${job.label}**`,
    `Materia: ${job.filtros.materiaCodigo}`,
    `Turno: ${job.filtros.turno}`,
    `Dias: ${job.filtros.dias.join(', ')}`,
    `Sedes excluidas: ${sedes}`,
    `Estado: ${formatStatus(job, user)}`,
    `Ultimo sondeo: ${lastPoll}`,
    `Ultimo resultado: ${lastOutcome}`,
  ].join('\n');
}

export const estadoCommand = {
  data: new SlashCommandBuilder()
    .setName('estado')
    .setDescription('Ver tus busquedas activas o pausadas'),

  async execute(interaction, { db = getDb() } = {}) {
    const jobs = listJobsByUser(db, interaction.user.id);
    if (jobs.length === 0) {
      await interaction.reply({ content: 'No tenes busquedas activas.', ephemeral: true });
      return;
    }

    const user = getUser(db, interaction.user.id);
    await interaction.reply({
      content: jobs.map((job) => formatJob(job, user)).join('\n\n'),
      ephemeral: true,
    });
  },
};
