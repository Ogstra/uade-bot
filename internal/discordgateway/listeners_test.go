package discordgateway

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/disgoorg/disgo/discord"
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
	dispatcher := fakeDispatcher{fn: func(context.Context, discordhttp.Interaction) (discordhttp.InteractionResponse, error) {
		panic("boom")
	}}

	captured := make(chan discordhttp.InteractionResponse, 1)
	fakeRespond := func(_ context.Context, id, token string, response discordhttp.InteractionResponse) error {
		captured <- response
		return nil
	}

	dispatchAndRespond(dispatcher, "id", "tok", discordhttp.Interaction{}, fakeRespond)

	select {
	case response := <-captured:
		if contentOf(t, response) != internalErrorMessage {
			t.Fatalf("response content = %q, want %q", contentOf(t, response), internalErrorMessage)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for dispatchAndRespond to recover from panic and respond")
	}
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
