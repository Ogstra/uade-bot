import { MessageFlags, SlashCommandBuilder } from 'discord.js';
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

  async execute(interaction, { db = getDb(), env, onCredentialsUpdated } = {}) {
    // Must ack within Discord's 3s interaction window -- runCredentialRotation
    // waits on real human DM replies (up to DEFAULT_TIMEOUT_MS = 120s each),
    // so a bare `interaction.reply()` after that wait is way too late: Discord
    // already shows "The application did not respond" and the reply itself
    // fails with a stale-interaction error (10062), even though the DM flow
    // and credential save underneath completed successfully.
    await interaction.deferReply({ flags: MessageFlags.Ephemeral });
    upsertUser(db, interaction.user.id);
    const result = await runCredentialRotation(interaction, {
      db,
      env,
      onCredentialsUpdated,
    });
    await interaction.editReply(result.message);
  },
};
