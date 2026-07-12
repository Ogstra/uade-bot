import { SlashCommandBuilder } from 'discord.js';
import { getDb } from '../../db/database.js';
import { upsertUser } from '../../db/users.repository.js';
import { runCredentialRotation } from '../credentials-flow.js';

export const credencialesCommand = {
  data: new SlashCommandBuilder()
    .setName('credenciales')
    .setDescription('Cargar o actualizar tus credenciales de UADE por DM')
    .addStringOption((option) =>
      option
        .setName('modo')
        .setDescription('Que queres actualizar')
        .setRequired(false)
        .addChoices(
          { name: 'Usuario y password', value: 'usuario_password' },
          { name: 'Link de inscripcion', value: 'link' },
          { name: 'Todo', value: 'todo' },
        ),
    ),

  async execute(interaction, { db = getDb(), env } = {}) {
    upsertUser(db, interaction.user.id);
    const result = await runCredentialRotation(interaction, { db, env });
    await interaction.reply({ content: result.message, ephemeral: true });
  },
};
