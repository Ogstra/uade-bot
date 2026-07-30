package discordgateway

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"

	"github.com/ogs/uade-bot/internal/dashboard"
	"github.com/ogs/uade-bot/internal/discordhttp"
)

const ephemeral = 1 << 6
const interactionDeadline = 2400 * time.Millisecond
const timeoutMessage = "La operación tardó demasiado. Volvé a intentar en unos segundos."
const internalErrorMessage = "Ocurrio un error interno."

// respondTimeout bounds the outbound interaction-callback POST
// (defaultRespond/respondContext) with its own fresh deadline, independent
// of the interaction's own (possibly already-expired) ctx, so a stalled
// Discord endpoint cannot leak the goroutine/connection indefinitely
// (03.3-REVIEW.md WR-04).
var respondTimeout = 5 * time.Second

// Dispatcher is the subset of discordhttp.CommandDispatcher that
// dispatchAndRespond needs. discordhttp.CommandDispatcher already satisfies
// this interface implicitly, so no call site constructing/passing a
// CommandDispatcher value requires any change.
type Dispatcher interface {
	DispatchInteraction(context.Context, discordhttp.Interaction) (discordhttp.InteractionResponse, error)
}

type guildSource interface {
	GuildsForEach(func(discord.Guild))
}

// Projection exposes the bounded, cache-only Discord data required by the
// dashboard. Guilds remain owned by disgo's FlagGuilds cache; only identities
// observed on interactions are retained here.
type Projection struct {
	mu         sync.RWMutex
	guilds     guildSource
	identities map[string]string
}

func NewProjection(source guildSource) *Projection {
	return &Projection{guilds: source, identities: make(map[string]string)}
}

func (p *Projection) SetGuildSource(source guildSource) {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.guilds = source
	p.mu.Unlock()
}

func (p *Projection) Observe(guildID snowflake.ID, member *discord.ResolvedMember, user discord.User) {
	if p == nil || user.ID == 0 {
		return
	}
	name := ""
	if member != nil && member.Nick != nil {
		name = strings.TrimSpace(*member.Nick)
	}
	if name == "" {
		name = strings.TrimSpace(user.EffectiveName())
	}
	if name == "" {
		name = user.ID.String()
	}
	p.mu.Lock()
	p.identities[user.ID.String()] = name
	p.mu.Unlock()
}

func (p *Projection) DisplayNames() map[string]string {
	out := map[string]string{}
	if p == nil {
		return out
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	for id, name := range p.identities {
		out[id] = name
	}
	return out
}

func (p *Projection) Guilds() []dashboard.Guild {
	if p == nil {
		return []dashboard.Guild{}
	}
	p.mu.RLock()
	source := p.guilds
	p.mu.RUnlock()
	out := []dashboard.Guild{}
	if source != nil {
		source.GuildsForEach(func(guild discord.Guild) {
			out = append(out, dashboard.Guild{ID: guild.ID.String(), Name: guild.Name})
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name == out[j].Name {
			return out[i].ID < out[j].ID
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func projectionOf(values []*Projection) *Projection {
	if len(values) == 0 {
		return nil
	}
	return values[0]
}

func observeInteraction(p *Projection, guildID *snowflake.ID, member *discord.ResolvedMember, user discord.User) {
	if p == nil || guildID == nil {
		return
	}
	p.Observe(*guildID, member, user)
}

// respondFunc sends a fully-built interaction response back to Discord.
// dispatchAndRespond accepts one as a parameter so tests can substitute a
// fake instead of performing a real network call.
type respondFunc func(ctx context.Context, id, token string, response discordhttp.InteractionResponse) error

// respondContext returns a context bounded by respondTimeout, always a
// fresh deadline independent of the interaction's own ctx.
func respondContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), respondTimeout)
}

// defaultRespond is the production respondFunc: it posts the interaction
// callback to Discord's real REST API, bounded by respondContext.
func defaultRespond(_ context.Context, id, token string, response discordhttp.InteractionResponse) error {
	ctx, cancel := respondContext()
	defer cancel()
	return Respond(ctx, nil, "", id, token, response)
}

// OnSlashCommand adapts a Gateway ApplicationCommandInteractionCreate event
// into a discordhttp.Interaction and dispatches it through dispatcher.
func OnSlashCommand(dispatcher Dispatcher, projections ...*Projection) func(*events.ApplicationCommandInteractionCreate) {
	return func(e *events.ApplicationCommandInteractionCreate) {
		observeInteraction(projectionOf(projections), e.GuildID(), e.Member(), e.User())
		in := base(e.GuildID(), e.ChannelID(), e.Member(), e.User())
		in.Type = 2
		in.Data = discordhttp.InteractionData{
			Name:    e.SlashCommandInteractionData().CommandName(),
			Options: mapSlashOptions(e.SlashCommandInteractionData().All()),
		}
		dispatchAndRespond(dispatcher, e.ID().String(), e.Token(), in, defaultRespond)
	}
}

// OnModalSubmit adapts a Gateway ModalSubmitInteractionCreate event into a
// discordhttp.Interaction and dispatches it through dispatcher.
func OnModalSubmit(dispatcher Dispatcher, projections ...*Projection) func(*events.ModalSubmitInteractionCreate) {
	return func(e *events.ModalSubmitInteractionCreate) {
		observeInteraction(projectionOf(projections), e.GuildID(), e.Member(), e.User())
		in := base(e.GuildID(), e.ChannelID(), e.Member(), e.User())
		in.Type = 5
		in.Data = discordhttp.InteractionData{
			CustomID: e.Data.CustomID,
			Values:   modalValuesJSON(e.Data.Components),
		}
		dispatchAndRespond(dispatcher, e.ID().String(), e.Token(), in, defaultRespond)
	}
}

// OnAutocomplete adapts a Gateway AutocompleteInteractionCreate event into a
// discordhttp.Interaction and dispatches it through dispatcher.
func OnAutocomplete(dispatcher Dispatcher, projections ...*Projection) func(*events.AutocompleteInteractionCreate) {
	return func(e *events.AutocompleteInteractionCreate) {
		observeInteraction(projectionOf(projections), e.GuildID(), e.Member(), e.User())
		in := base(e.GuildID(), e.ChannelID(), e.Member(), e.User())
		in.Type = 4
		in.Data = discordhttp.InteractionData{
			Name:    e.Data.CommandName,
			Options: mapAutocompleteOptions(e.Data.All()),
		}
		dispatchAndRespond(dispatcher, e.ID().String(), e.Token(), in, defaultRespond)
	}
}

// OnComponentInteraction adapts a Gateway message-component interaction into
// the transport-agnostic shape handled by discordhttp.CommandDispatcher.
func OnComponentInteraction(dispatcher Dispatcher, projections ...*Projection) func(*events.ComponentInteractionCreate) {
	return func(e *events.ComponentInteractionCreate) {
		observeInteraction(projectionOf(projections), e.GuildID(), e.Member(), e.User())
		in := base(e.GuildID(), e.ChannelID(), e.Member(), e.User())
		in.Type = 3
		in.Data = discordhttp.InteractionData{CustomID: e.Data.CustomID()}
		dispatchAndRespond(dispatcher, e.ID().String(), e.Token(), in, defaultRespond)
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

// dispatchResult carries DispatchInteraction's outcome (or a recovered
// panic, wrapped as err) across the completed channel to raceForResponse.
type dispatchResult struct {
	response discordhttp.InteractionResponse
	err      error
}

// raceForResponse implements the 2400ms deadline race: it returns
// whichever of completed or ctx.Done() resolves first, mapping a non-nil
// dispatchResult.err (including a recovered panic) to internalErrorMessage
// and a ctx deadline to timeoutMessage.
func raceForResponse(ctx context.Context, completed <-chan dispatchResult) discordhttp.InteractionResponse {
	select {
	case value := <-completed:
		if value.err != nil {
			return discordhttp.InteractionResponse{Type: 4, Data: map[string]any{"content": internalErrorMessage, "flags": ephemeral}}
		}
		return value.response
	case <-ctx.Done():
		return discordhttp.InteractionResponse{Type: 4, Data: map[string]any{"content": timeoutMessage, "flags": ephemeral}}
	}
}

// dispatchAndRespond spawns the goroutine required so a slow DB/UADE call
// never blocks disgo's synchronous event dispatch (which would stall the
// shared Gateway heartbeat), reproducing internal/discordhttp/handler.go's
// goroutine + context.WithTimeout(2400ms) + select deadline pattern
// verbatim. A panic inside dispatcher.DispatchInteraction is recovered so
// it never crashes the whole process (03.3-REVIEW.md CR-01); the outbound
// callback POST (via respond) is bounded by its own fresh respondTimeout,
// independent of this interaction's own (possibly already-expired) ctx
// (03.3-REVIEW.md WR-04).
func dispatchAndRespond(dispatcher Dispatcher, id, token string, in discordhttp.Interaction, respond respondFunc) {
	go func() {
		if err := runInteractionPipeline(dispatcher, id, token, in, respond); err != nil {
			log.Print("interaction pipeline failed")
		}
	}()
}

// runInteractionPipeline is the outer recovery boundary for the complete
// asynchronous interaction path: deadline construction, dispatch race, and
// response callback. It never includes interaction data, tokens, payloads, or
// recovered panic values in errors or logs.
func runInteractionPipeline(dispatcher Dispatcher, id, token string, in discordhttp.Interaction, respond respondFunc) (err error) {
	responseStarted := false
	defer func() {
		if recover() == nil {
			return
		}
		log.Print("interaction pipeline panic recovered")
		err = errors.New("interaction pipeline panic recovered")
		if responseStarted {
			return
		}
		responseStarted = true
		response := discordhttp.InteractionResponse{Type: 4, Data: map[string]any{"content": internalErrorMessage, "flags": ephemeral}}
		if respondErr := respondSafely(respond, context.Background(), id, token, response); respondErr != nil {
			log.Print("interaction pipeline recovery response failed")
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), interactionDeadline)
	defer cancel()

	completed := make(chan dispatchResult, 1)
	go func() {
		defer func() {
			if recover() != nil {
				log.Print("interaction dispatch panic recovered")
				completed <- dispatchResult{err: errors.New("interaction dispatch panic recovered")}
			}
		}()
		response, dispatchErr := dispatcher.DispatchInteraction(ctx, in)
		completed <- dispatchResult{response: response, err: dispatchErr}
	}()

	response := raceForResponse(ctx, completed)
	responseStarted = true
	return respondSafely(respond, context.Background(), id, token, response)
}

// respondSafely contains both ordinary response errors and callback panics.
// A recovered panic becomes a fixed error and log marker; it is never retried
// because the callback may already have acknowledged the interaction.
func respondSafely(respond respondFunc, ctx context.Context, id, token string, response discordhttp.InteractionResponse) (err error) {
	defer func() {
		if recover() != nil {
			log.Print("interaction response panic recovered")
			err = errors.New("interaction response panic recovered")
		}
	}()
	return respond(ctx, id, token, response)
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
