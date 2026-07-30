package discordgateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"

	"github.com/ogs/uade-bot/internal/discordhttp"
)

// fakeDispatcher satisfies Dispatcher for tests, delegating to fn so each
// test can control DispatchInteraction's behavior (including panicking)
// without a real database or network call.
type fakeDispatcher struct {
	fn func(context.Context, discordhttp.Interaction) (discordhttp.InteractionResponse, error)
}

func (f fakeDispatcher) DispatchInteraction(ctx context.Context, in discordhttp.Interaction) (discordhttp.InteractionResponse, error) {
	return f.fn(ctx, in)
}

func contentOf(t *testing.T, response discordhttp.InteractionResponse) string {
	t.Helper()
	data, ok := response.Data.(map[string]any)
	if !ok {
		t.Fatalf("response.Data = %#v, want map[string]any", response.Data)
	}
	content, _ := data["content"].(string)
	return content
}

type signalLogBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
	marker string
	once   sync.Once
	seen   chan struct{}
}

func newSignalLogBuffer(marker string) *signalLogBuffer {
	return &signalLogBuffer{marker: marker, seen: make(chan struct{})}
}

func (b *signalLogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n, err := b.buffer.Write(p)
	if strings.Contains(b.buffer.String(), b.marker) {
		b.once.Do(func() { close(b.seen) })
	}
	return n, err
}

func (b *signalLogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

func captureGatewayLog(t *testing.T, marker string) *signalLogBuffer {
	t.Helper()
	output := newSignalLogBuffer(marker)
	originalWriter := log.Writer()
	originalFlags := log.Flags()
	originalPrefix := log.Prefix()
	log.SetOutput(output)
	log.SetFlags(0)
	log.SetPrefix("")
	t.Cleanup(func() {
		log.SetOutput(originalWriter)
		log.SetFlags(originalFlags)
		log.SetPrefix(originalPrefix)
	})
	return output
}

func assertGatewayRecoverySanitized(t *testing.T, value string) {
	t.Helper()
	for _, secret := range []string{"discord-secret-sentinel", "Bot.ABC123", "UadePass!123", "interaction-token-sentinel", "modal-value-sentinel"} {
		if strings.Contains(value, secret) {
			t.Fatalf("recovery log leaked %q: %q", secret, value)
		}
	}
}

func TestRaceForResponseReturnsCompletedResponseWhenDispatcherFinishesBeforeDeadline(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	want := discordhttp.InteractionResponse{Type: 4, Data: map[string]any{"content": "ok"}}
	completed := make(chan dispatchResult, 1)
	completed <- dispatchResult{response: want}

	got := raceForResponse(ctx, completed)
	if contentOf(t, got) != "ok" {
		t.Fatalf("raceForResponse = %+v, want content=ok", got)
	}
}

func TestRaceForResponseReturnsTimeoutMessageWhenContextExpiresFirst(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	completed := make(chan dispatchResult)
	got := raceForResponse(ctx, completed)
	if contentOf(t, got) != timeoutMessage {
		t.Fatalf("raceForResponse content = %q, want %q", contentOf(t, got), timeoutMessage)
	}
}

func TestRaceForResponseReturnsInternalErrorMessageWhenDispatcherErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	completed := make(chan dispatchResult, 1)
	completed <- dispatchResult{err: context.DeadlineExceeded}

	got := raceForResponse(ctx, completed)
	if contentOf(t, got) != internalErrorMessage {
		t.Fatalf("raceForResponse content = %q, want %q", contentOf(t, got), internalErrorMessage)
	}
}

func TestDispatchAndRespondRecoversFromDispatcherPanicAndStillResponds(t *testing.T) {
	logs := captureGatewayLog(t, "interaction dispatch panic recovered")
	dispatcher := fakeDispatcher{fn: func(context.Context, discordhttp.Interaction) (discordhttp.InteractionResponse, error) {
		panic("discord-secret-sentinel token=Bot.ABC123 modal_password=UadePass!123 modal-value-sentinel")
	}}

	captured := make(chan discordhttp.InteractionResponse, 1)
	fakeRespond := func(_ context.Context, id, token string, response discordhttp.InteractionResponse) error {
		captured <- response
		return nil
	}

	dispatchAndRespond(dispatcher, "id", "interaction-token-sentinel", discordhttp.Interaction{}, fakeRespond)

	select {
	case response := <-captured:
		if contentOf(t, response) != internalErrorMessage {
			t.Fatalf("response content = %q, want %q", contentOf(t, response), internalErrorMessage)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for dispatchAndRespond to recover from panic and respond")
	}
	select {
	case <-logs.seen:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for sanitized dispatch recovery log")
	}
	assertGatewayRecoverySanitized(t, logs.String())
}

func TestDispatchAndRespondRecoversFromResponderPanicAndServesNextInteraction(t *testing.T) {
	logs := captureGatewayLog(t, "interaction response panic recovered")
	dispatcher := fakeDispatcher{fn: func(context.Context, discordhttp.Interaction) (discordhttp.InteractionResponse, error) {
		return discordhttp.InteractionResponse{Type: 4, Data: map[string]any{"content": "ok"}}, nil
	}}

	var attempts atomic.Int32
	secondResponse := make(chan discordhttp.InteractionResponse, 1)
	fakeRespond := func(_ context.Context, _, _ string, response discordhttp.InteractionResponse) error {
		if attempts.Add(1) == 1 {
			panic("discord-secret-sentinel token=Bot.ABC123 modal_password=UadePass!123")
		}
		secondResponse <- response
		return nil
	}

	dispatchAndRespond(dispatcher, "first-id", "interaction-token-sentinel", discordhttp.Interaction{}, fakeRespond)
	select {
	case <-logs.seen:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for response panic recovery")
	}

	dispatchAndRespond(dispatcher, "second-id", "interaction-token-sentinel", discordhttp.Interaction{}, fakeRespond)
	select {
	case response := <-secondResponse:
		if contentOf(t, response) != "ok" {
			t.Fatalf("second response content = %q, want ok", contentOf(t, response))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for interaction after responder panic")
	}
	if attempts.Load() != 2 {
		t.Fatalf("respond attempts=%d, want exactly 2", attempts.Load())
	}
	assertGatewayRecoverySanitized(t, logs.String())
}

func TestRespondContextEnforcesRespondTimeoutBound(t *testing.T) {
	original := respondTimeout
	defer func() { respondTimeout = original }()
	respondTimeout = 50 * time.Millisecond

	ctx, cancel := respondContext()
	defer cancel()

	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("respondContext() returned a context with no deadline")
	}
	remaining := time.Until(deadline)
	if remaining <= 0 || remaining > respondTimeout {
		t.Fatalf("time.Until(deadline) = %v, want (0, %v]", remaining, respondTimeout)
	}
}

func TestPermissionsStringUsesDecimalBitmaskNotHumanReadableName(t *testing.T) {
	if got := permissionsString(nil); got != "0" {
		t.Fatalf("permissionsString(nil) = %q, want 0", got)
	}
	member := &discord.ResolvedMember{Permissions: 8}
	if got := permissionsString(member); got != "8" {
		t.Fatalf("permissionsString(&{Permissions:8}) = %q, want 8", got)
	}
}

func TestMapSlashOptionsDecodesRawValues(t *testing.T) {
	got := mapSlashOptions([]discord.SlashCommandOption{{Name: "cod_materia", Type: 3, Value: json.RawMessage(`"3.1.050"`)}})
	want := []discordhttp.Option{{Name: "cod_materia", Type: 3, Value: "3.1.050"}}
	if len(got) != 1 || got[0] != want[0] {
		t.Fatalf("mapSlashOptions = %+v, want %+v", got, want)
	}
}

func TestMapAutocompleteOptionsRoundTripsFocused(t *testing.T) {
	got := mapAutocompleteOptions([]discord.AutocompleteOption{{Name: "busqueda", Type: 3, Value: json.RawMessage(`"7"`), Focused: true}})
	if len(got) != 1 || got[0].Name != "busqueda" || got[0].Value != "7" || !got[0].Focused {
		t.Fatalf("mapAutocompleteOptions = %+v", got)
	}
}

func TestModalValuesJSONProducesCommandsGoDecodeableShape(t *testing.T) {
	raw := modalValuesJSON(map[string]discord.InteractiveComponent{
		"uade_username": discord.TextInputComponent{CustomID: "uade_username", Value: "u1"},
	})

	// Mirror internal/discordhttp/commands.go's modalValues decode target
	// exactly, to prove the two are compatible without importing an
	// unexported helper across packages.
	var rows []struct {
		Components []struct {
			CustomID string `json:"custom_id"`
			Value    string `json:"value"`
		} `json:"components"`
	}
	if err := json.Unmarshal(raw, &rows); err != nil {
		t.Fatal(err)
	}
	out := make(map[string]string)
	for _, row := range rows {
		for _, field := range row.Components {
			out[field.CustomID] = field.Value
		}
	}
	if out["uade_username"] != "u1" {
		t.Fatalf("decoded = %+v, want uade_username=u1", out)
	}
}

func TestGuildIDStringHandlesNilAndPopulated(t *testing.T) {
	if got := guildIDString(nil); got != "" {
		t.Fatalf("guildIDString(nil) = %q, want empty", got)
	}
	id := snowflake.ID(123456789012345678)
	if got := guildIDString(&id); got != id.String() {
		t.Fatalf("guildIDString(&id) = %q, want %q", got, id.String())
	}
}

type fakeGuildCache struct {
	guilds []discord.Guild
}

func (f *fakeGuildCache) GuildsForEach(fn func(discord.Guild)) {
	for _, guild := range f.guilds {
		fn(guild)
	}
}

func TestGatewayProjectionGuildAndIdentityPrecedence(t *testing.T) {
	global := "Nombre global"
	nick := "Apodo **guild** <@9>"
	otherNick := "Otro apodo"
	guilds := &fakeGuildCache{guilds: []discord.Guild{
		{ID: snowflake.ID(2), Name: "Zulu"},
		{ID: snowflake.ID(1), Name: "Alpha"},
	}}
	projection := NewProjection(guilds)
	projection.Observe(snowflake.ID(2), &discord.ResolvedMember{Member: discord.Member{Nick: &nick}}, discord.User{
		ID: snowflake.ID(10), Username: "usuario", GlobalName: &global,
	})
	projection.Observe(snowflake.ID(1), &discord.ResolvedMember{Member: discord.Member{Nick: &otherNick}}, discord.User{
		ID: snowflake.ID(10), Username: "usuario", GlobalName: &global,
	})

	gotGuilds := projection.Guilds()
	if len(gotGuilds) != 2 || gotGuilds[0].Name != "Alpha" || gotGuilds[1].Name != "Zulu" {
		t.Fatalf("Guilds() = %+v, want stable name/ID order", gotGuilds)
	}
	if got := projection.DisplayNames()[snowflake.ID(10).String()]; got != otherNick {
		t.Fatalf("DisplayNames()[10] = %q, want deterministic nickname %q", got, otherNick)
	}
	if got := projection.ResolveIdentity("1", "10"); got != otherNick {
		t.Fatalf("ResolveIdentity(guild 1,user 10) = %q, want %q", got, otherNick)
	}
	if got := projection.ResolveIdentity("2", "10"); got != nick {
		t.Fatalf("ResolveIdentity(guild 2,user 10) = %q, want %q", got, nick)
	}

	projection.Observe(snowflake.ID(2), &discord.ResolvedMember{}, discord.User{
		ID: snowflake.ID(11), Username: "usuario", GlobalName: &global,
	})
	if got := projection.DisplayNames()[snowflake.ID(11).String()]; got != global {
		t.Fatalf("DisplayNames()[11] = %q, want global name %q", got, global)
	}
	projection.Observe(snowflake.ID(2), nil, discord.User{ID: snowflake.ID(12), Username: "usuario"})
	if got := projection.DisplayNames()[snowflake.ID(12).String()]; got != "usuario" {
		t.Fatalf("DisplayNames()[12] = %q, want username", got)
	}

	copyNames := projection.DisplayNames()
	copyNames[snowflake.ID(10).String()] = "mutado"
	if got := projection.DisplayNames()[snowflake.ID(10).String()]; got != otherNick {
		t.Fatalf("DisplayNames returned mutable internal state: %q", got)
	}

	projection.Observe(snowflake.ID(2), nil, discord.User{ID: snowflake.ID(13)})
	if got := projection.ResolveIdentity("2", "13"); got != "13" {
		t.Fatalf("empty identity fallback = %q, want raw ID once", got)
	}
}

func TestGatewayProjectionGuildLifecycleUnavailableLeaveAndRejoin(t *testing.T) {
	projection := NewProjection(nil)
	guild := discord.Guild{ID: snowflake.ID(77), Name: "Zulu"}
	generic := func(value discord.Guild) *events.GenericGuild {
		return &events.GenericGuild{GuildID: value.ID, Guild: value}
	}
	nick := "Nickname"
	projection.Observe(guild.ID, &discord.ResolvedMember{Member: discord.Member{Nick: &nick}}, discord.User{ID: snowflake.ID(10), Username: "user"})

	OnGuildJoin(projection)(&events.GuildJoin{GenericGuild: generic(guild)})
	if got := projection.Guilds(); len(got) != 1 || got[0].Name != "Zulu" {
		t.Fatalf("join guilds=%+v", got)
	}
	OnGuildUnavailable(projection)(&events.GuildUnavailable{GenericGuild: generic(guild)})
	if got := projection.Guilds(); len(got) != 1 {
		t.Fatalf("unavailable removed guild: %+v", got)
	}
	updated := guild
	updated.Name = "Alpha"
	OnGuildUpdate(projection)(&events.GuildUpdate{GenericGuild: generic(updated), OldGuild: guild})
	if got := projection.Guilds(); len(got) != 1 || got[0].Name != "Alpha" {
		t.Fatalf("update guilds=%+v", got)
	}
	OnGuildLeave(projection)(&events.GuildLeave{GenericGuild: generic(updated)})
	if got := projection.Guilds(); len(got) != 0 {
		t.Fatalf("leave guilds=%+v", got)
	}
	if got := projection.ResolveIdentity("77", "10"); got != "user" {
		t.Fatalf("leave retained guild nickname: %q", got)
	}
	OnGuildJoin(projection)(&events.GuildJoin{GenericGuild: generic(guild)})
	OnGuildJoin(projection)(&events.GuildJoin{GenericGuild: generic(guild)})
	if got := projection.Guilds(); len(got) != 1 || got[0].ID != "77" {
		t.Fatalf("rejoin duplicated guild: %+v", got)
	}
}

func TestGatewayProjectionConcurrentLiveIdentityUpdates(t *testing.T) {
	projection := NewProjection(nil)
	guild := snowflake.ID(4)
	user := discord.User{ID: snowflake.ID(5), Username: "initial"}
	projection.Observe(guild, nil, user)
	consumer := projection
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			name := fmt.Sprintf("nick-%02d", i)
			projection.Observe(guild, &discord.ResolvedMember{Member: discord.Member{Nick: &name}}, user)
		}(i)
		go func() {
			defer wg.Done()
			_ = consumer.ResolveIdentity("4", "5")
			_ = consumer.DisplayNames()
			_ = consumer.Guilds()
		}()
	}
	wg.Wait()
	late := "late"
	projection.Observe(guild, &discord.ResolvedMember{Member: discord.Member{Nick: &late}}, user)
	if got := consumer.ResolveIdentity("4", "5"); got != late {
		t.Fatalf("live consumer got %q, want late update", got)
	}
}
