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

func genericMessageCreate(content string, components []discord.ContainerComponent) discord.MessageCreate {
	return discord.MessageCreate{
		Content:    content,
		Components: components,
		AllowedMentions: &discord.AllowedMentions{
			Parse: []discord.AllowedMentionType{},
			Roles: []snowflake.ID{},
			Users: []snowflake.ID{},
		},
	}
}

func dmMessageCreate(content, nonce string, components []discord.ContainerComponent) discord.MessageCreate {
	message := genericMessageCreate(content, components)
	message.Nonce = nonce
	message.EnforceNonce = true
	return message
}

func mentionMessageCreate(owner, content, nonce string, components []discord.ContainerComponent) (discord.MessageCreate, error) {
	ownerID, err := snowflake.Parse(owner)
	if err != nil {
		return discord.MessageCreate{}, err
	}
	message := dmMessageCreate(content, nonce, components)
	message.AllowedMentions.Users = []snowflake.ID{ownerID}
	return message, nil
}

func (n Notifier) SendDM(user, content, nonce string, components ...discord.ContainerComponent) error {
	id, err := snowflake.Parse(user)
	if err != nil {
		return err
	}
	channel, err := n.Users.CreateDMChannel(id)
	if err != nil {
		return err
	}
	_, err = n.Rest.CreateMessage(channel.ID(), dmMessageCreate(content, nonce, components))
	return err
}

func (n Notifier) SendMention(channel, owner, content, nonce string, components ...discord.ContainerComponent) error {
	id, err := snowflake.Parse(channel)
	if err != nil {
		return err
	}
	message, err := mentionMessageCreate(owner, content, nonce, components)
	if err != nil {
		return err
	}
	_, err = n.Rest.CreateMessage(id, message)
	return err
}

// Send preserves the generic channel-message API used by command responses.
// Notification fan-out uses SendMention so only its structural owner mention
// is allowlisted; generic content denies every mention type.
func (n Notifier) Send(channel, content string, components ...discord.ContainerComponent) error {
	id, err := snowflake.Parse(channel)
	if err != nil {
		return err
	}
	_, err = n.Rest.CreateMessage(id, genericMessageCreate(content, components))
	return err
}
