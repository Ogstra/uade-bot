package discordgateway

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"

	"github.com/ogs/uade-bot/internal/discordhttp"
)

const ephemeral = 1 << 6
const interactionDeadline = 2400 * time.Millisecond
const timeoutMessage = "La operación tardó demasiado. Volvé a intentar en unos segundos."
const internalErrorMessage = "Ocurrio un error interno."

// OnSlashCommand adapts a Gateway ApplicationCommandInteractionCreate event
// into a discordhttp.Interaction and dispatches it through dispatcher.
func OnSlashCommand(dispatcher discordhttp.CommandDispatcher) func(*events.ApplicationCommandInteractionCreate) {
	return func(e *events.ApplicationCommandInteractionCreate) {
		in := base(e.GuildID(), e.ChannelID(), e.Member(), e.User())
		in.Type = 2
		in.Data = discordhttp.InteractionData{
			Name:    e.SlashCommandInteractionData().CommandName(),
			Options: mapSlashOptions(e.SlashCommandInteractionData().All()),
		}
		dispatchAndRespond(dispatcher, e.ID().String(), e.Token(), in)
	}
}

// OnModalSubmit adapts a Gateway ModalSubmitInteractionCreate event into a
// discordhttp.Interaction and dispatches it through dispatcher.
func OnModalSubmit(dispatcher discordhttp.CommandDispatcher) func(*events.ModalSubmitInteractionCreate) {
	return func(e *events.ModalSubmitInteractionCreate) {
		in := base(e.GuildID(), e.ChannelID(), e.Member(), e.User())
		in.Type = 5
		in.Data = discordhttp.InteractionData{
			CustomID: e.Data.CustomID,
			Values:   modalValuesJSON(e.Data.Components),
		}
		dispatchAndRespond(dispatcher, e.ID().String(), e.Token(), in)
	}
}

// OnAutocomplete adapts a Gateway AutocompleteInteractionCreate event into a
// discordhttp.Interaction and dispatches it through dispatcher.
func OnAutocomplete(dispatcher discordhttp.CommandDispatcher) func(*events.AutocompleteInteractionCreate) {
	return func(e *events.AutocompleteInteractionCreate) {
		in := base(e.GuildID(), e.ChannelID(), e.Member(), e.User())
		in.Type = 4
		in.Data = discordhttp.InteractionData{
			Name:    e.Data.CommandName,
			Options: mapAutocompleteOptions(e.Data.All()),
		}
		dispatchAndRespond(dispatcher, e.ID().String(), e.Token(), in)
	}
}

// base builds the transport-agnostic fields shared by every interaction type:
// guild/channel identifiers and the member/user identity DispatchInteraction
// uses for ownership/admin checks.
func base(guildID *snowflake.ID, channelID snowflake.ID, member *discord.ResolvedMember, user discord.User) discordhttp.Interaction {
	return discordhttp.Interaction{
		GuildID:   guildIDString(guildID),
		ChannelID: channelID.String(),
		Member:    discordhttp.Member{Permissions: permissionsString(member), User: discordhttp.User{ID: user.ID.String()}},
	}
}

// dispatchAndRespond spawns the goroutine required so a slow DB/UADE call
// never blocks disgo's synchronous event dispatch (which would stall the
// shared Gateway heartbeat), reproducing internal/discordhttp/handler.go's
// goroutine + context.WithTimeout(2400ms) + select deadline pattern
// verbatim. The interaction's own bounded ctx may already be near its
// deadline once the callback POST fires, so Respond uses a fresh unbounded
// context for the egress call itself.
func dispatchAndRespond(dispatcher discordhttp.CommandDispatcher, id, token string, in discordhttp.Interaction) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), interactionDeadline)
		defer cancel()

		type result struct {
			response discordhttp.InteractionResponse
			err      error
		}
		completed := make(chan result, 1)
		go func() {
			response, err := dispatcher.DispatchInteraction(ctx, in)
			completed <- result{response: response, err: err}
		}()

		var response discordhttp.InteractionResponse
		select {
		case value := <-completed:
			response = value.response
			if value.err != nil {
				response = discordhttp.InteractionResponse{Type: 4, Data: map[string]any{"content": internalErrorMessage, "flags": ephemeral}}
			}
		case <-ctx.Done():
			response = discordhttp.InteractionResponse{Type: 4, Data: map[string]any{"content": timeoutMessage, "flags": ephemeral}}
		}
		_ = Respond(context.Background(), nil, "", id, token, response)
	}()
}

// permissionsString formats a resolved member's permission bitmask as the
// decimal string internal/discordhttp's hasAdministrator expects. It
// deliberately does not use Permissions.String(), which renders a
// human-readable name list (e.g. "Administrator") rather than a bitmask.
func permissionsString(member *discord.ResolvedMember) string {
	if member == nil {
		return "0"
	}
	return strconv.FormatInt(int64(member.Permissions), 10)
}

// mapSlashOptions converts disgo's typed slash-command options into
// discordhttp.Option, decoding each raw JSON value the same way the HTTP
// webhook transport's JSON body would have.
func mapSlashOptions(all []discord.SlashCommandOption) []discordhttp.Option {
	out := make([]discordhttp.Option, 0, len(all))
	for _, o := range all {
		var value any
		_ = json.Unmarshal(o.Value, &value)
		out = append(out, discordhttp.Option{Name: o.Name, Type: int(o.Type), Value: value})
	}
	return out
}

// mapAutocompleteOptions converts disgo's typed autocomplete options into
// discordhttp.Option, preserving which option is currently focused.
func mapAutocompleteOptions(all []discord.AutocompleteOption) []discordhttp.Option {
	out := make([]discordhttp.Option, 0, len(all))
	for _, o := range all {
		var value any
		_ = json.Unmarshal(o.Value, &value)
		out = append(out, discordhttp.Option{Name: o.Name, Type: int(o.Type), Value: value, Focused: o.Focused})
	}
	return out
}

// modalValuesJSON rebuilds the single-row `[{"components":[{"custom_id",
// "value"}, ...]}]` shape internal/discordhttp's modalValues helper decodes,
// from disgo's typed modal component map. One row containing every field is
// equivalent to Discord's own multi-row action-row layout for that decode
// target.
func modalValuesJSON(components map[string]discord.InteractiveComponent) json.RawMessage {
	type field struct {
		CustomID string `json:"custom_id"`
		Value    string `json:"value"`
	}
	type row struct {
		Components []field `json:"components"`
	}
	fields := make([]field, 0, len(components))
	for _, component := range components {
		if text, ok := component.(discord.TextInputComponent); ok {
			fields = append(fields, field{CustomID: text.CustomID, Value: text.Value})
		}
	}
	out, err := json.Marshal([]row{{Components: fields}})
	if err != nil {
		return json.RawMessage("[]")
	}
	return out
}

// guildIDString formats a possibly-nil guild snowflake as internal/discordhttp
// expects: "" for DMs (no guild), else the decimal snowflake string.
func guildIDString(id *snowflake.ID) string {
	if id == nil {
		return ""
	}
	return id.String()
}
