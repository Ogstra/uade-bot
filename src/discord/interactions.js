import { isFromAuthorizedGuild, isGuildInteraction } from './access-control.js';
import logger from '../logger.js';

const GENERIC_ERROR = 'No pude procesar ese comando. Proba de nuevo en unos minutos.';

function isDispatchableInteraction(interaction) {
  return interaction.isChatInputCommand?.() || interaction.isAutocomplete?.();
}

async function sendGenericError(interaction) {
  if (interaction.isAutocomplete?.()) {
    return;
  }

  if (interaction.deferred || interaction.replied) {
    await interaction.editReply(GENERIC_ERROR);
    return;
  }

  await interaction.reply({ content: GENERIC_ERROR, ephemeral: true });
}

export function createInteractionHandler({
  commandsByName,
  env,
  logger: handlerLogger = logger,
} = {}) {
  return async function handleInteraction(interaction) {
    if (!isDispatchableInteraction(interaction)) {
      return;
    }

    if (
      isGuildInteraction(interaction) &&
      !isFromAuthorizedGuild(interaction, env.DISCORD_GUILD_ID)
    ) {
      return;
    }

    const command = commandsByName.get(interaction.commandName);
    if (!command) {
      return;
    }

    try {
      if (interaction.isAutocomplete?.()) {
        if (command.autocomplete) {
          await command.autocomplete(interaction);
        } else {
          await interaction.respond([]);
        }
        return;
      }

      if (interaction.isChatInputCommand?.()) {
        await command.execute(interaction);
      }
    } catch (err) {
      handlerLogger.error(
        { event: 'discord_interaction_failed', commandName: interaction.commandName, message: err.message },
        'Discord interaction failed',
      );
      await sendGenericError(interaction);
    }
  };
}
