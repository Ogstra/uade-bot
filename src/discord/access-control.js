export function isGuildInteraction(interaction) {
  return typeof interaction?.guildId === 'string' && interaction.guildId.length > 0;
}

export function isFromAuthorizedGuild(interaction, authorizedGuildId) {
  return isGuildInteraction(interaction) && interaction.guildId === authorizedGuildId;
}
