package discordgateway

import (
	"encoding/json"
	"testing"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"

	"github.com/ogs/uade-bot/internal/discordhttp"
)

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
