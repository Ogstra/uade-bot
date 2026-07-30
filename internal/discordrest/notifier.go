package discordrest

import (
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"
)

type Notifier struct {
	Rest  rest.Channels
	Users rest.Users
}

func New(token string) Notifier {
	client := rest.NewClient(token)
	return Notifier{Rest: rest.NewChannels(client), Users: rest.NewUsers(client)}
}

func VacancyActionRow(jobID string) discord.ActionRowComponent {
	return discord.NewActionRow(discord.NewDangerButton("Detener busqueda", "detener_job:"+jobID))
}

func (n Notifier) SendDM(user, content string, components ...discord.ContainerComponent) error {
	id, err := snowflake.Parse(user)
	if err != nil {
		return err
	}
	channel, err := n.Users.CreateDMChannel(id)
	if err != nil {
		return err
	}
	_, err = n.Rest.CreateMessage(channel.ID(), discord.MessageCreate{Content: content, Components: components})
	return err
}
func (n Notifier) Send(channel string, content string, components ...discord.ContainerComponent) error {
	id, e := snowflake.Parse(channel)
	if e != nil {
		return e
	}
	_, e = n.Rest.CreateMessage(id, discord.MessageCreate{Content: content, Components: components})
	return e
}
