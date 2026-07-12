import { PermissionFlagsBits } from 'discord.js';

export function isGuildInteraction(interaction) {
  return typeof interaction?.guildId === 'string' && interaction.guildId.length > 0;
}

/**
 * @param {import('discord.js').Interaction} interaction
 * @param {string[]} authorizedGuildIds
 * @returns {boolean}
 */
export function isFromAuthorizedGuild(interaction, authorizedGuildIds) {
  return isGuildInteraction(interaction) && authorizedGuildIds.includes(interaction.guildId);
}

/**
 * Gates admin-only commands (`adminOnly: true` on the command definition,
 * checked centrally in interactions.js). Uses Discord's own server
 * permission system -- `interaction.memberPermissions` is computed by
 * Discord and sent with every guild interaction payload, no GUILD_MEMBERS
 * privileged intent required -- so admin access is whoever the server
 * owner has granted the Administrator permission to via normal Discord
 * role management, not a hardcoded user id in .env.
 *
 * @param {import('discord.js').Interaction} interaction
 * @returns {boolean}
 */
export function isAdminInteraction(interaction) {
  return Boolean(interaction.memberPermissions?.has(PermissionFlagsBits.Administrator));
}
