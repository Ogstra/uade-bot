import { Client, GatewayIntentBits, Options, Partials } from 'discord.js';

export function createDiscordClient() {
  return new Client({
    intents: [GatewayIntentBits.Guilds, GatewayIntentBits.DirectMessages],
    partials: [Partials.Channel],
    // This bot never reads message history, guild member lists, presences,
    // reactions, threads, or voice state -- only commands/DMs/notifications
    // -- so discord.js's default (effectively unbounded) caches for those
    // managers are pure memory overhead. Guilds/Users/Channels stay at
    // default since interactions.js and notifications.js actively use them.
    makeCache: Options.cacheWithLimits({
      ...Options.DefaultMakeCacheSettings,
      MessageManager: 0,
      GuildMemberManager: 0,
      PresenceManager: 0,
      ReactionManager: 0,
      ThreadManager: 0,
      ThreadMemberManager: 0,
      StageInstanceManager: 0,
      VoiceStateManager: 0,
    }),
  });
}
