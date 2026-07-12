import { isFromAuthorizedGuild, isGuildInteraction } from './access-control.js';
import { handleDetenerButton, isDetenerButton } from './components.js';
import { getDb } from '../db/database.js';
import { logCommandUsage } from '../db/command-log.repository.js';
import logger from '../logger.js';
import { genericInteractionErrorMessage } from './messages.js';

function isDispatchableInteraction(interaction) {
  return interaction.isChatInputCommand?.() || interaction.isAutocomplete?.() || interaction.isButton?.();
}

async function sendGenericError(interaction) {
  if (interaction.isAutocomplete?.()) {
    return;
  }

  if (interaction.deferred || interaction.replied) {
    await interaction.editReply(genericInteractionErrorMessage());
    return;
  }

  await interaction.reply({ content: genericInteractionErrorMessage(), ephemeral: true });
}

export function createInteractionHandler({
  commandsByName,
  env,
  logger: handlerLogger = logger,
  commandContext = {},
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

    try {
      if (interaction.isButton?.()) {
        if (isDetenerButton(interaction)) {
          await handleDetenerButton(interaction, commandContext);
        }
        return;
      }

      const command = commandsByName.get(interaction.commandName);
      if (!command) {
        return;
      }

      if (interaction.isAutocomplete?.()) {
        if (command.autocomplete) {
          await command.autocomplete(interaction, commandContext);
        } else {
          await interaction.respond([]);
        }
        return;
      }

      if (interaction.isChatInputCommand?.()) {
        logCommandUsage(commandContext.db ?? getDb(), {
          discordUserId: interaction.user.id,
          commandName: interaction.commandName,
          guildId: interaction.guildId ?? null,
        });
        await command.execute(interaction, commandContext);
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
