package discordhttp

// Slash-command replies are cards for the same reason notifications are: a
// colored, titled block is scannable in a busy channel and tells the user at
// a glance whether something worked. The palette is Discord's own, matching
// the DM cards built in internal/discordrest.
type cardStyle struct {
	Title string
	Color int
}

var (
	cardInfo    = cardStyle{Title: "UADE Bot", Color: 0x5865f2}
	cardSuccess = cardStyle{Title: "Listo", Color: 0x57f287}
	cardWarning = cardStyle{Title: "Atención", Color: 0xfee75c}
	cardDanger  = cardStyle{Title: "No pude hacerlo", Color: 0xed4245}
)

// card renders an ephemeral embed reply. The body is NOT blockquoted (unlike
// the notification cards): command replies include lists of searches and
// multi-line tables where quoting every line only adds noise.
func card(style cardStyle, content string) InteractionResponse {
	return InteractionResponse{Type: 4, Data: map[string]any{
		"embeds": []any{map[string]any{
			"title":       style.Title,
			"description": content,
			"color":       style.Color,
		}},
		"flags":            ephemeral,
		"allowed_mentions": map[string]any{"parse": []string{}},
	}}
}
