package discordrest

import (
	"strings"
	"testing"
)

func TestCardMessageRendersAnEmbedInsteadOfPlainContent(t *testing.T) {
	message := cardMessageCreate(CardVacancy, "**Materia:** Analisis\n**Cupos:** 2", "", nil)

	if message.Content != "" {
		t.Fatalf("Content = %q, want empty: the body belongs to the embed", message.Content)
	}
	if len(message.Embeds) != 1 {
		t.Fatalf("Embeds = %d, want 1", len(message.Embeds))
	}
	embed := message.Embeds[0]
	if embed.Title != CardVacancy.Title {
		t.Fatalf("Title = %q, want %q", embed.Title, CardVacancy.Title)
	}
	if embed.Color != CardVacancy.Color {
		t.Fatalf("Color = %d, want %d", embed.Color, CardVacancy.Color)
	}
	if !strings.Contains(embed.Description, "**Materia:** Analisis") {
		t.Fatalf("Description lost the body: %q", embed.Description)
	}
	if embed.Timestamp == nil {
		t.Fatal("embed has no timestamp: cards are dated so an old alert is recognizable as old")
	}
}

// Every line is quoted, which is what visually separates the data block from
// the title in the Discord client.
func TestCardMessageQuotesEveryBodyLine(t *testing.T) {
	message := cardMessageCreate(CardPaused, "primera\nsegunda", "", nil)

	for _, line := range strings.Split(message.Embeds[0].Description, "\n") {
		if !strings.HasPrefix(line, "> ") {
			t.Fatalf("unquoted line %q in %q", line, message.Embeds[0].Description)
		}
	}
}

// Mentions must stay inert in cards exactly as they are in plain messages:
// UADE-controlled strings (materia names) end up inside the description.
func TestCardMessageKeepsMentionsInert(t *testing.T) {
	message := cardMessageCreate(CardInfo, "@everyone", "", nil)

	if message.AllowedMentions == nil || len(message.AllowedMentions.Parse) != 0 {
		t.Fatalf("AllowedMentions = %+v, want an empty parse list", message.AllowedMentions)
	}
}

// The nonce drives Discord's own duplicate suppression, which the
// notification pipeline relies on for at-most-once delivery.
func TestCardMessageKeepsNonceEnforcement(t *testing.T) {
	message := cardMessageCreate(CardVacancy, "body", "nonce-1", nil)

	if message.Nonce != "nonce-1" || !message.EnforceNonce {
		t.Fatalf("nonce=%q enforce=%v, want nonce-1/true", message.Nonce, message.EnforceNonce)
	}
}
