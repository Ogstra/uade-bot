import { isAdminInteraction, isFromAuthorizedGuild, isGuildInteraction } from './access-control.js';
import { handleDetenerButton, isDetenerButton } from './components.js';
import { getDb } from '../db/database.js';
import { logCommandUsage } from '../db/command-log.repository.js';
import logger from '../logger.js';
import { genericInteractionErrorMessage, notAuthorizedMessage } from './messages.js';

function isDispatchableInteraction(interaction) {
  return interaction.isChatInputCommand?.() || interaction.isAutocomplete?.() || interaction.isButton?.();
}

/**
 * Best-effort fallback reply after a command/button handler throws. Never
 * lets its own failure escape uncaught: `interaction.deferred`/`.replied`
 * reflect this process's local view of the interaction, which can
 * disagree with Discord's server-side state under network flakiness (our
 * own earlier reply/deferReply attempt can time out locally while Discord
 * still processes it, so a retry here gets rejected with 40060 "already
 * acknowledged"). `handleInteraction`'s caller is a bare `client.on(...)`
 * listener with no surrounding catch, so an uncaught rejection here would
 * crash the whole bot process over a single failed interaction reply.
 */
async function sendGenericError(interaction, handlerLogger) {
  if (interaction.isAutocomplete?.()) {
    return;
  }

  try {
    if (interaction.deferred || interaction.replied) {
      await interaction.editReply(genericInteractionErrorMessage());
      return;
    }

    await interaction.reply({ content: genericInteractionErrorMessage(), ephemeral: true });
  } catch (err) {
    handlerLogger.error(
      { event: 'generic_error_reply_failed', commandName: interaction.commandName, message: err.message },
      'Failed to send the generic error fallback reply',
    );
  }
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

      if (command.adminOnly && !isAdminInteraction(interaction)) {
        if (interaction.isAutocomplete?.()) {
          // Silently empty, not an error reply -- an autocomplete listing
          // every account's jobs would itself leak data to a non-admin
          // before they even see a permission error.
          await interaction.respond([]);
        } else {
          await interaction.reply({ content: notAuthorizedMessage(), ephemeral: true });
        }
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
      await sendGenericError(interaction, handlerLogger);
    }
  };
}
