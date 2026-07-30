package discordrest

import (
	"reflect"
	"testing"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"
)

func TestMessageCreateDeniesMentionsByDefault(t *testing.T) {
	message := dmMessageCreate("hostile @everyone <@123456789>", "uade-0123456789abcdef0123", nil)
	if message.Nonce != "uade-0123456789abcdef0123" || !message.EnforceNonce {
		t.Fatalf("nonce/enforce = %q/%v", message.Nonce, message.EnforceNonce)
	}
	if message.AllowedMentions == nil {
		t.Fatal("AllowedMentions = nil")
	}
	if len(message.AllowedMentions.Parse) != 0 || len(message.AllowedMentions.Roles) != 0 || len(message.AllowedMentions.Users) != 0 {
		t.Fatalf("DM allowed mentions = %#v, want deny all", message.AllowedMentions)
	}
}

func TestMessageCreateMentionAllowsOnlyOwner(t *testing.T) {
	message, err := mentionMessageCreate("123456789", "<@123456789> hostile @everyone <@987654321>", "uade-abcdef0123456789abcd", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !message.EnforceNonce || message.AllowedMentions == nil {
		t.Fatalf("enforce/allowed mentions = %v/%#v", message.EnforceNonce, message.AllowedMentions)
	}
	if len(message.AllowedMentions.Parse) != 0 || len(message.AllowedMentions.Roles) != 0 {
		t.Fatalf("parse/roles = %#v/%#v, want empty", message.AllowedMentions.Parse, message.AllowedMentions.Roles)
	}
	wantUsers := []snowflake.ID{snowflake.ID(123456789)}
	if !reflect.DeepEqual(message.AllowedMentions.Users, wantUsers) {
		t.Fatalf("users = %#v, want %#v", message.AllowedMentions.Users, wantUsers)
	}
}

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
