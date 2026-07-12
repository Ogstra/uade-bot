import { SlashCommandBuilder } from 'discord.js';
import { getDb } from '../../db/database.js';
import { getCredentials } from '../../db/credentials.repository.js';
import { createJob, listJobsByUser } from '../../db/jobs.repository.js';
import { getUser, upsertUser } from '../../db/users.repository.js';
import { FiltrosSchema } from '../../schemas.js';
import { runFullCredentialOnboarding } from '../credentials-flow.js';
import { searchCreatedMessage, searchValidationError, tooManySearchesMessage } from '../messages.js';
import { MAX_ACTIVE_SEARCHES_PER_USER } from './job-selection.js';
import logger from '../../logger.js';

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
    materiaCodigo: options.getString('cod_materia'),
    turno: options.getString('turno'),
    ofrecimiento: options.getString('ofrecimiento'),
    dias: parseDias(options.getString('dias')).filter((dia) => VALID_DIAS.has(dia)),
    sedesExcluidas: parseCsv(options.getString('sedes_excluidas')),
  });
}

export const buscarCommand = {
  data: new SlashCommandBuilder()
    .setName('buscar')
    .setDescription('Crear una busqueda de vacantes en UADE')
    .addStringOption((option) =>
      option
        .setName('cod_materia')
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

  async execute(
    interaction,
    {
      db = getDb(),
      env,
      credentialOnboarding = runFullCredentialOnboarding,
      onJobCreated,
      ephemeralReplies = false,
    } = {},
  ) {
    await interaction.deferReply({ ephemeral: ephemeralReplies });

    let filtros;
    try {
      filtros = parseFiltrosFromOptions(interaction.options);
    } catch (err) {
      await interaction.editReply(searchValidationError(err));
      return;
    }

    if (listJobsByUser(db, interaction.user.id).length >= MAX_ACTIVE_SEARCHES_PER_USER) {
      await interaction.editReply(tooManySearchesMessage(MAX_ACTIVE_SEARCHES_PER_USER));
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
    let credentialResult;
    if (!hadCredentials) {
      credentialResult = await credentialOnboarding(interaction, { db, env });
    }

    await interaction.editReply(
      searchCreatedMessage({
        job,
        filtros,
        pauseReason: user?.pauseReason,
        credentialResult,
        requestedCredentials: !hadCredentials,
      }),
    );

    if ((hadCredentials || credentialResult?.ok) && onJobCreated) {
      try {
        await Promise.resolve(onJobCreated(job));
      } catch (err) {
        logger.error(
          { event: 'immediate_poll_enqueue_failed', jobId: job.id, message: err.message },
          'Immediate poll enqueue failed',
        );
      }
    }
  },
};
