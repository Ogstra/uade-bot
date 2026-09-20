package discordrest

import (
	"strings"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"
)

// Card is the visual frame around an outgoing message: a titled, colored
// embed with the body quoted underneath. The bot's unsolicited messages all
// arrive in DMs mixed into the user's normal conversations, so a bare line of
// text is easy to scroll past -- a colored card is not, and the color alone
// says whether something was found, something is waiting, or something needs
// attention.
type Card struct {
	Title string
	Color int
}

// Discord's own role palette, so the cards look native in the client rather
// than like a third-party theme.
var (
	CardVacancy = Card{Title: "Vacante encontrada", Color: 0x57f287}
	CardPaused  = Card{Title: "Búsquedas en pausa", Color: 0xfee75c}
	CardResumed = Card{Title: "Búsquedas reactivadas", Color: 0x57f287}
	CardAction  = Card{Title: "Acción requerida", Color: 0xed4245}
	CardInfo    = Card{Title: "UADE Bot", Color: 0x5865f2}
)

// quoteBody puts every line behind a blockquote marker, including empty ones,
// so a multi-vacancy body reads as a single quoted block instead of breaking
// out of the quote at each blank separator line.
func quoteBody(body string) string {
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		lines[i] = "> " + line
	}
	return strings.Join(lines, "\n")
}

// cardMessageCreate renders body as an embed. Content is left empty on
// purpose: duplicating the text outside the embed would show it twice in the
// client. AllowedMentions stays as restrictive as the plain-text path --
// materia names come from UADE and land inside the description.
func cardMessageCreate(card Card, body, nonce string, components []discord.ContainerComponent) discord.MessageCreate {
	now := time.Now()
	message := genericMessageCreate("", components)
	message.Embeds = []discord.Embed{{
		Title:       card.Title,
		Description: quoteBody(body),
		Color:       card.Color,
		Timestamp:   &now,
	}}
	if nonce != "" {
		message.Nonce = nonce
		message.EnforceNonce = true
	}
	return message
}

// SendDMCard is SendDM's carded twin: same delivery semantics (DM channel,
// nonce-enforced duplicate suppression), different presentation.
func (n Notifier) SendDMCard(user string, card Card, content, nonce string, components ...discord.ContainerComponent) error {
	id, err := snowflake.Parse(user)
	if err != nil {
		return err
	}
	channel, err := n.Users.CreateDMChannel(id)
	if err != nil {
		return err
	}
	_, err = n.Rest.CreateMessage(channel.ID(), cardMessageCreate(card, content, nonce, components))
	return err
}

// SendMentionCard mirrors SendMention: the owner is the only allowlisted
// mention, so a channel notification pings its owner and nobody else.
func (n Notifier) SendMentionCard(channel, owner string, card Card, content, nonce string, components ...discord.ContainerComponent) error {
	channelID, err := snowflake.Parse(channel)
	if err != nil {
		return err
	}
	ownerID, err := snowflake.Parse(owner)
	if err != nil {
		return err
	}
	message := cardMessageCreate(card, content, nonce, components)
	message.AllowedMentions.Users = []snowflake.ID{ownerID}
	// The mention has to live outside the embed: Discord does not ping from
	// embed bodies, so a card-only notification would never reach the user.
	message.Content = "<@" + owner + ">"
	_, err = n.Rest.CreateMessage(channelID, message)
	return err
}
