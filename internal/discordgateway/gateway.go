package discordgateway

import (
	"github.com/disgoorg/disgo"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/gateway"

	"github.com/ogs/uade-bot/internal/discordhttp"
)

// New boots a single Discord Gateway session that carries both presence and
// interaction handling: slash commands, modal submits and autocomplete are
// all delivered as Gateway events (INTERACTION_CREATE and its typed
// sub-events) rather than via an inbound HTTP interactions endpoint, per
// 03.3-CONTEXT.md D-19/D-20.
//
// gateway.IntentGuilds is the only intent requested and is non-privileged:
// INTERACTION_CREATE is a passthrough event Discord delivers regardless of
// configured intents, and member.permissions already arrives resolved in
// the interaction payload, so no cache.FlagRoles/cache.FlagMembers or
// privileged IntentGuildMembers is needed.
func New(token string, dispatcher discordhttp.CommandDispatcher) (bot.Client, error) {
	return disgo.New(token,
		bot.WithGatewayConfigOpts(gateway.WithIntents(gateway.IntentGuilds)),
		bot.WithEventListenerFunc(OnSlashCommand(dispatcher)),
		bot.WithEventListenerFunc(OnModalSubmit(dispatcher)),
		bot.WithEventListenerFunc(OnAutocomplete(dispatcher)),
		bot.WithEventListenerFunc(OnComponentInteraction(dispatcher)),
	)
}
