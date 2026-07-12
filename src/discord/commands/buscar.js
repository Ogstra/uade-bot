import { SlashCommandBuilder } from 'discord.js';
import { ZodError } from 'zod';
import { getDb } from '../../db/database.js';
import { getCredentials } from '../../db/credentials.repository.js';
import { createJob } from '../../db/jobs.repository.js';
import { getUser, upsertUser } from '../../db/users.repository.js';
import { FiltrosSchema } from '../../schemas.js';
import { runFullCredentialOnboarding } from '../credentials-flow.js';

const TURNO_CHOICES = ['Mañana', 'Tarde', 'Noche', 'Intensivo', 'Online'];
const OFRECIMIENTO_CHOICES = [
  { name: 'Curricular', value: 'curricular' },
  { name: 'Optativa', value: 'optativa' },
];
const VALID_DIAS = new Set(['LU', 'MA', 'MI', 'JU', 'VI', 'SA']);

function parseCsv(value) {
  return String(value ?? '')
    .split(',')
    .map((part) => part.trim())
    .filter(Boolean);
}

function parseDias(value) {
  return parseCsv(value).map((dia) => dia.toUpperCase());
}

function parseFiltrosFromOptions(options) {
  return FiltrosSchema.parse({
    materiaCodigo: options.getString('materia'),
    turno: options.getString('turno'),
    ofrecimiento: options.getString('ofrecimiento'),
    dias: parseDias(options.getString('dias')).filter((dia) => VALID_DIAS.has(dia)),
    sedesExcluidas: parseCsv(options.getString('sedes_excluidas')),
  });
}

function validationMessage(err) {
  if (err instanceof ZodError) {
    const invalidMateria = err.issues.some((issue) => issue.path.includes('materiaCodigo'));
    if (invalidMateria) {
      return 'El codigo de materia tiene que tener formato N.N.NNN, por ejemplo 3.1.050.';
    }
  }

  return 'No pude validar esos filtros. Revisa materia, dias, turno y ofrecimiento.';
}

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
    )
    .addStringOption((option) =>
      option
        .setName('etiqueta')
        .setDescription('Nombre corto para reconocer esta busqueda')
        .setRequired(false),
    ),

  async execute(interaction, { db = getDb(), env, credentialOnboarding = runFullCredentialOnboarding } = {}) {
    await interaction.deferReply({ ephemeral: true });

    let filtros;
    try {
      filtros = parseFiltrosFromOptions(interaction.options);
    } catch (err) {
      await interaction.editReply(validationMessage(err));
      return;
    }

    upsertUser(db, interaction.user.id);
    const hadCredentials = getCredentials(db, interaction.user.id) !== null;
    const label = interaction.options.getString('etiqueta') || filtros.materiaCodigo;
    const job = createJob(db, {
      discordUserId: interaction.user.id,
      filtros,
      channelId: interaction.channelId ?? null,
      label,
    });
    const user = getUser(db, interaction.user.id);
    const sedes = filtros.sedesExcluidas.length > 0 ? filtros.sedesExcluidas.join(', ') : 'ninguna';
    const base =
      `Busqueda creada: ${job.label}\n` +
      `Materia: ${filtros.materiaCodigo}\n` +
      `Turno: ${filtros.turno}\n` +
      `Ofrecimiento: ${filtros.ofrecimiento}\n` +
      `Dias: ${filtros.dias.join(', ')}\n` +
      `Sedes excluidas: ${sedes}`;
    const pausedSuffix = user?.pauseReason
      ? `\nQuedo creada pausada por el estado de tu cuenta: ${user.pauseReason}.`
      : '';
    let credentialSuffix = '';
    if (!hadCredentials) {
      const result = await credentialOnboarding(interaction, { db, env });
      credentialSuffix = result.ok
        ? '\nComo no tenias credenciales guardadas, te las pedi por DM y quedaron guardadas.'
        : `\n${result.message}`;
    }

    await interaction.editReply(`${base}${pausedSuffix}${credentialSuffix}`);
  },
};
