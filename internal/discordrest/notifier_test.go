package discordrest

import (
	"testing"

	"github.com/disgoorg/disgo/discord"
)

func TestNotifierConstructs(t *testing.T) { _ = New("") }

func TestVacancyActionRow(t *testing.T) {
	row := VacancyActionRow("42")
	buttons := row.Buttons()
	if len(buttons) != 1 {
		t.Fatalf("buttons = %d, want 1", len(buttons))
	}
	button := buttons[0]
	if button.CustomID != "detener_job:42" {
		t.Fatalf("custom id = %q, want detener_job:42", button.CustomID)
	}
	if button.Style != discord.ButtonStyleDanger {
		t.Fatalf("style = %v, want danger", button.Style)
	}
	if button.Label != "Detener busqueda" {
		t.Fatalf("label = %q, want Detener busqueda", button.Label)
	}
}
