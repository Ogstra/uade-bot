package discordhttp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Ogstra/uade-bot/internal/entra"
)

type fakeMicrosoft struct {
	begin   entra.LoginStatus
	status  entra.Status
	began   []string
	forgot  []string
	beginFn func(string) (entra.LoginStatus, error)
}

func (f *fakeMicrosoft) BeginLogin(_ context.Context, userID string) (entra.LoginStatus, error) {
	f.began = append(f.began, userID)
	if f.beginFn != nil {
		return f.beginFn(userID)
	}
	return f.begin, nil
}
func (f *fakeMicrosoft) Status(string) (entra.Status, error) { return f.status, nil }
func (f *fakeMicrosoft) Forget(userID string) error {
	f.forgot = append(f.forgot, userID)
	return nil
}

func pendingLogin() entra.LoginStatus {
	return entra.LoginStatus{
		State:           entra.LoginPending,
		UserCode:        "ABCD-EFGH",
		VerificationURI: "https://microsoft.com/devicelogin",
		ExpiresAt:       time.Date(2026, 10, 27, 21, 15, 0, 0, time.UTC),
	}
}

func microsoftCommand(user, accion string) map[string]any {
	var options []map[string]any
	if accion != "" {
		options = []map[string]any{{"name": "accion", "type": 3, "value": accion}}
	}
	return command(user, "microsoft", "0", options)
}

// Any user can sign in: no admin row, no permissions bit, still gets a code.
func TestMicrosoftSignInIsOpenToEveryUser(t *testing.T) {
	d := testDispatcher(t)
	fake := &fakeMicrosoft{begin: pendingLogin()}
	d.Microsoft = fake

	text := responseContent(dispatchJSON(t, d, microsoftCommand("444444444444444444", "iniciar")))

	if len(fake.began) != 1 || fake.began[0] != "444444444444444444" {
		t.Fatalf("BeginLogin calls = %v, want one for the caller", fake.began)
	}
	for _, want := range []string{"ABCD-EFGH", "https://microsoft.com/devicelogin", "18:15"} {
		if !strings.Contains(text, want) {
			t.Fatalf("reply missing %q: %s", want, text)
		}
	}
}

func TestMicrosoftDefaultsToSignInWithoutAnAction(t *testing.T) {
	d := testDispatcher(t)
	fake := &fakeMicrosoft{begin: pendingLogin()}
	d.Microsoft = fake

	dispatchJSON(t, d, microsoftCommand("444444444444444444", ""))

	if len(fake.began) != 1 {
		t.Fatalf("BeginLogin calls = %d, want 1", len(fake.began))
	}
}

// The code goes only to whoever asked: someone else reading it in a shared
// channel could finish the sign-in first and bind their own account.
func TestMicrosoftReplyIsEphemeral(t *testing.T) {
	d := testDispatcher(t)
	d.Microsoft = &fakeMicrosoft{begin: pendingLogin()}

	resp := dispatchJSON(t, d, microsoftCommand("444444444444444444", "iniciar"))

	data, _ := resp.Data.(map[string]any)
	if data["flags"] != ephemeral {
		t.Fatalf("flags = %v, want ephemeral", data["flags"])
	}
}

// Nothing a reply can carry may be a token, nor the device_code (the secret
// half of the grant). The fake deliberately puts secrets where a careless
// implementation could pick them up.
func TestMicrosoftRepliesNeverCarryTokens(t *testing.T) {
	d := testDispatcher(t)
	login := pendingLogin()
	login.Message = "irrelevant"
	d.Microsoft = &fakeMicrosoft{begin: login, status: entra.Status{State: entra.StateActive, ExpiresAt: time.Now().Add(time.Hour)}}

	for _, accion := range []string{"iniciar", "estado", "cerrar"} {
		raw, _ := json.Marshal(dispatchJSON(t, d, microsoftCommand("444444444444444444", accion)))
		for _, secret := range []string{"access_token", "refresh_token", "device_code", "Bearer"} {
			if strings.Contains(string(raw), secret) {
				t.Fatalf("%s reply contains %q: %s", accion, secret, raw)
			}
		}
	}
}

func TestMicrosoftReportsWhenNotConfigured(t *testing.T) {
	d := testDispatcher(t) // Microsoft left nil

	text := responseContent(dispatchJSON(t, d, microsoftCommand("444444444444444444", "iniciar")))

	if !strings.Contains(text, "no está configurado") {
		t.Fatalf("reply = %q", text)
	}
}

func TestMicrosoftSurfacesASummarizedFailure(t *testing.T) {
	d := testDispatcher(t)
	d.Microsoft = &fakeMicrosoft{begin: entra.LoginStatus{State: entra.LoginFailed, Message: "No pude comunicarme con Microsoft. Probá de nuevo en un rato."}}

	text := responseContent(dispatchJSON(t, d, microsoftCommand("444444444444444444", "iniciar")))

	if !strings.Contains(text, "No pude comunicarme con Microsoft") {
		t.Fatalf("reply = %q", text)
	}
}

func TestMicrosoftStatusReflectsEachState(t *testing.T) {
	for state, want := range map[entra.State]string{
		entra.StateActive: "activa",
		entra.StateDead:   "venció",
		entra.StateNone:   "No tenés",
	} {
		d := testDispatcher(t)
		d.Microsoft = &fakeMicrosoft{status: entra.Status{State: state}}

		text := responseContent(dispatchJSON(t, d, microsoftCommand("444444444444444444", "estado")))

		if !strings.Contains(text, want) {
			t.Fatalf("%s: reply = %q, want it to mention %q", state, text, want)
		}
	}
}

// Signing out only ever touches the caller's own session.
func TestMicrosoftSignOutForgetsOnlyTheCaller(t *testing.T) {
	d := testDispatcher(t)
	fake := &fakeMicrosoft{}
	d.Microsoft = fake

	dispatchJSON(t, d, microsoftCommand("555555555555555555", "cerrar"))

	if len(fake.forgot) != 1 || fake.forgot[0] != "555555555555555555" {
		t.Fatalf("Forget calls = %v", fake.forgot)
	}
}
