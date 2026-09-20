package discordhttp

import "testing"

// Every command reply is an embed: plain content would render as a bare line
// of text with no color and no title, which is exactly what this change
// exists to replace.
func TestCommandRepliesAreCards(t *testing.T) {
	data, ok := message("hola").Data.(map[string]any)
	if !ok {
		t.Fatalf("Data type = %T, want map", message("hola").Data)
	}
	if content, exists := data["content"]; exists && content != "" {
		t.Fatalf("content = %v, want the body inside the embed instead", content)
	}
	embeds, _ := data["embeds"].([]any)
	if len(embeds) != 1 {
		t.Fatalf("embeds = %d, want 1", len(embeds))
	}
	embed, _ := embeds[0].(map[string]any)
	if embed["description"] != "hola" {
		t.Fatalf("description = %v, want hola", embed["description"])
	}
	if embed["color"] != cardInfo.Color {
		t.Fatalf("color = %v, want the neutral card color", embed["color"])
	}
}

// Replies stay ephemeral and mention-inert: a card is a presentation change,
// not a change to who can see the reply or get pinged by it.
func TestCardsKeepEphemeralAndInertMentions(t *testing.T) {
	data, _ := card(cardDanger, "@everyone").Data.(map[string]any)

	if data["flags"] != ephemeral {
		t.Fatalf("flags = %v, want ephemeral", data["flags"])
	}
	mentions, _ := data["allowed_mentions"].(map[string]any)
	parse, _ := mentions["parse"].([]string)
	if len(parse) != 0 {
		t.Fatalf("allowed_mentions.parse = %v, want empty", parse)
	}
}

// The four styles must stay visually distinct: a confirmation that renders
// in the same color as a refusal defeats the point of carding replies.
func TestCardStylesAreDistinct(t *testing.T) {
	seen := map[int]string{}
	for _, style := range []cardStyle{cardInfo, cardSuccess, cardWarning, cardDanger} {
		if other, clash := seen[style.Color]; clash {
			t.Fatalf("%q reuses the color of %q", style.Title, other)
		}
		seen[style.Color] = style.Title
	}
}
