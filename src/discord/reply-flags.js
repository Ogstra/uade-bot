import { MessageFlags } from 'discord.js';

/**
 * discord.js deprecated the boolean `ephemeral` reply/deferReply option in
 * favor of `flags: MessageFlags.Ephemeral` -- unlike the old boolean, `flags`
 * has no "off" value to pass, so a runtime toggle (e.g.
 * `env.DISCORD_EPHEMERAL_REPLIES`) needs the key omitted entirely for the
 * public case. Spread this into a reply/deferReply payload instead of the
 * old `ephemeral: someBoolean`.
 *
 * @param {boolean} ephemeral
 * @returns {{ flags: number } | {}}
 */
export function ephemeralFlags(ephemeral) {
  return ephemeral ? { flags: MessageFlags.Ephemeral } : {};
}
