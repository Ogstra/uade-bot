package discordrest

import (
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"
)

type Notifier struct{ Rest rest.Channels }

func New(token string) Notifier {
	client := rest.NewClient(token)
	return Notifier{Rest: rest.NewChannels(client)}
}
func (n Notifier) Send(channel string, content string) error {
	id, e := snowflake.Parse(channel)
	if e != nil {
		return e
	}
	_, e = n.Rest.CreateMessage(id, discord.MessageCreate{Content: content})
	return e
}
