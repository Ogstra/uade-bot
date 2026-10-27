package main

import (
	"errors"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Ogstra/uade-bot/internal/discordrest"
	"github.com/Ogstra/uade-bot/internal/entra"
)

// entraEnv is the full set of variables that turn Microsoft sign-in on.
// Either all three required ones are present or the feature stays off; a
// partial set is reported instead of guessed at.
type entraEnv struct {
	TenantID, Resource, Secret, ClientID, Dir, ProbeURL string
}

func readEntraEnv(getenv func(string) string, dbPath string) (entraEnv, bool, error) {
	env := entraEnv{
		TenantID: strings.TrimSpace(getenv("ENTRA_TENANT_ID")),
		Resource: strings.TrimSpace(getenv("ENTRA_RESOURCE")),
		Secret:   getenv("ENTRA_TOKEN_SECRET"),
		ClientID: strings.TrimSpace(getenv("ENTRA_CLIENT_ID")),
		Dir:      strings.TrimSpace(getenv("ENTRA_DIR")),
		ProbeURL: strings.TrimSpace(getenv("ENTRA_VALIDATE_URL")),
	}
	set := 0
	for _, v := range []string{env.TenantID, env.Resource, env.Secret} {
		if v != "" {
			set++
		}
	}
	if set == 0 {
		return env, false, nil
	}
	if set < 3 {
		return env, false, errors.New("ENTRA_TENANT_ID, ENTRA_RESOURCE y ENTRA_TOKEN_SECRET van juntas; falta al menos una")
	}
	if len(env.Secret) < 32 {
		return env, false, errors.New("ENTRA_TOKEN_SECRET debe tener al menos 32 caracteres")
	}
	if env.Dir == "" {
		// Next to the database, so it lives on the same persistent volume and
		// inherits its ownership (data/ belongs to the service user).
		env.Dir = filepath.Join(filepath.Dir(dbPath), "entra")
	}
	if env.ProbeURL == "" {
		env.ProbeURL = entra.DefaultProbeURL(env.Resource)
	}
	return env, true, nil
}

// newEntraManager builds the per-user session manager. notify delivers the
// two unsolicited messages this feature sends: the outcome of a sign-in, and
// a session dying.
func newEntraManager(env entraEnv, notify func(user string, card discordrest.Card, text string)) (*entra.Manager, error) {
	cfg := entra.Config{TenantID: env.TenantID, Resource: env.Resource, ClientID: env.ClientID}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	httpClient := &http.Client{Timeout: 30 * time.Second}
	manager := &entra.Manager{
		Config:   cfg,
		HTTP:     httpClient,
		Dir:      env.Dir,
		Secret:   []byte(env.Secret),
		Validate: entra.ResourceValidator(httpClient, env.ProbeURL),
		OnLoginFinish: func(user string, status entra.LoginStatus) {
			if status.State == entra.LoginOK {
				notify(user, discordrest.Card{Title: "Sesión de Microsoft iniciada", Color: discordrest.CardVacancy.Color}, "Listo, ya quedaste conectado. La sesión se renueva sola: no tenés que volver a entrar.")
				return
			}
			notify(user, discordrest.Card{Title: "No se pudo iniciar sesión", Color: discordrest.CardAction.Color}, status.Message+"\n\nPodés reintentar con `/microsoft`.")
		},
		OnDead: func(user string) {
			notify(user, discordrest.Card{Title: "Tu sesión de Microsoft venció", Color: discordrest.CardAction.Color}, "Microsoft dejó de aceptar tu sesión. Usá `/microsoft` para volver a entrar con un código nuevo.")
		},
	}
	if err := manager.LoadAll(); err != nil {
		// Unreadable directory: run with no restored sessions rather than not
		// run at all. Users can sign in again.
		log.Printf("entra: could not restore stored sessions")
	}
	return manager, nil
}

func entraFromEnv(dbPath string, notify func(user string, card discordrest.Card, text string)) *entra.Manager {
	env, enabled, err := readEntraEnv(os.Getenv, dbPath)
	if err != nil {
		log.Printf("entra: inicio de sesión con Microsoft desactivado: %v", err)
		return nil
	}
	if !enabled {
		return nil
	}
	manager, err := newEntraManager(env, notify)
	if err != nil {
		log.Printf("entra: inicio de sesión con Microsoft desactivado: %v", err)
		return nil
	}
	log.Printf("entra: inicio de sesión con Microsoft activado (cliente %s)", clientLabel(env.ClientID))
	return manager
}

// clientLabel never prints the tenant or the secret, only which client kind is
// in use, because the Office fallback is worth noticing in the logs.
func clientLabel(clientID string) string {
	if clientID == "" || clientID == entra.OfficeClientID {
		return "público de Microsoft Office"
	}
	return "propio"
}
