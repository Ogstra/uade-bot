import { SlashCommandBuilder } from 'discord.js';

const TURNO_CHOICES = ['Mañana', 'Tarde', 'Noche', 'Intensivo', 'Online'];
const OFRECIMIENTO_CHOICES = [
  { name: 'Curricular', value: 'curricular' },
  { name: 'Optativa', value: 'optativa' },
];

export const buscarCommand = {
  data: new SlashCommandBuilder()
    .setName('buscar')
    .setDescription('Crear una busqueda de vacantes en UADE')
    .addStringOption((option) =>
      option
        .setName('materia')
        .setDescription('Codigo de materia, por ejemplo 3.1.050')
        .setRequired(true),
    )
    .addStringOption((option) =>
      option
        .setName('turno')
        .setDescription('Turno a buscar')
        .setRequired(true)
        .addChoices(...TURNO_CHOICES.map((turno) => ({ name: turno, value: turno }))),
    )
    .addStringOption((option) =>
      option
        .setName('ofrecimiento')
        .setDescription('Tipo de ofrecimiento')
        .setRequired(true)
        .addChoices(...OFRECIMIENTO_CHOICES),
    )
    .addStringOption((option) =>
      option
        .setName('dias')
        .setDescription('Dias separados por coma: LU,MA,MI,JU,VI,SA')
        .setRequired(true),
    )
    .addStringOption((option) =>
      option
        .setName('sedes_excluidas')
        .setDescription('Sedes a excluir, separadas por coma')
        .setRequired(false),
    ),

  async execute(interaction) {
    await interaction.deferReply({ ephemeral: true });

    await interaction.editReply(
      'La base de /buscar ya esta disponible. En el siguiente paso voy a guardar la busqueda y conectarla con tus credenciales.',
    );
  },
};
