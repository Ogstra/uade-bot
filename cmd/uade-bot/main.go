package main

import (
	"context"
	"database/sql"
	"errors"
	"github.com/ogs/uade-bot/internal/app"
	"github.com/ogs/uade-bot/internal/cutover"
	"github.com/ogs/uade-bot/internal/dashboard"
	"github.com/ogs/uade-bot/internal/discordgateway"
	"github.com/ogs/uade-bot/internal/discordhttp"
	"github.com/ogs/uade-bot/internal/shadow"
	"github.com/ogs/uade-bot/internal/store"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// allowedSSOPortalHost is the only host UADE_SSO_PORTAL_URL is permitted to
// resolve to. This is fail-closed by construction: an operator misconfig
// must never cause real UADE credentials to be sent to an arbitrary host
// (T-03.3-15-01).
const allowedSSOPortalHost = "inscripciones.uade.edu.ar"

// resolveSSOPortalURL returns the effective SSO portal URL for the process:
// raw if it parses and its host case-insensitively matches
// allowedSSOPortalHost, the documented default when raw is empty, or an
// error in every other case. Kept as a pure function (no log.Fatal, no env
// reads) so it is directly unit-testable from main_test.go.
func resolveSSOPortalURL(raw string) (string, error) {
	if raw == "" {
		raw = "https://" + allowedSSOPortalHost + "/"
	}
	parsed, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(parsed.Hostname(), allowedSSOPortalHost) {
		return "", errors.New("UADE_SSO_PORTAL_URL debe apuntar a " + allowedSSOPortalHost)
	}
	return raw, nil
}

func main() {
	cutoverConfig, err := cutover.FromEnv(os.Getenv)
	if err != nil {
		log.Fatal(err)
	}
	// Logged before the interlock so the audit line survives a later log.Fatal
	// (runtime mode validation, short dashboard secret, DB open failure).
	if notice := cutover.StandaloneNotice(cutoverConfig); notice != "" {
		log.Print(notice)
	}
	if err = cutover.CheckIfEnabled(cutoverConfig); err != nil {
		log.Fatal(err)
	}
	path := os.Getenv("UADE_DB_PATH")
	if path == "" {
		path = "data/uade.db"
	}
	mode := strings.ToLower(os.Getenv("UADE_RUNTIME_MODE"))
	if mode == "" {
		mode = "shadow"
	}
	if mode != "shadow" && mode != "active" && mode != "development" {
		log.Fatal("UADE_RUNTIME_MODE must be shadow, active or development")
	}
	if mode == "active" && !cutoverConfig.Enabled {
		log.Fatal("active runtime requires UADE_CUTOVER_ENABLED=true")
	}
	ssoPortalURL, ssoErr := resolveSSOPortalURL(os.Getenv("UADE_SSO_PORTAL_URL"))
	if ssoErr != nil {
		log.Fatal(ssoErr)
	}
	var db *sql.DB
	if mode == "shadow" {
		db, err = shadow.OpenReadOnly(path)
	} else {
		db, err = store.Open(path)
	}
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	addr := os.Getenv("DASHBOARD_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	user := os.Getenv("DASHBOARD_USERNAME")
	if user == "" {
		user = os.Getenv("DASHBOARD_USER")
	}
	sessionSecret := []byte(os.Getenv("DASHBOARD_SESSION_SECRET"))
	if len(sessionSecret) < 32 {
		log.Fatal("DASHBOARD_SESSION_SECRET must contain at least 32 bytes")
	}
	mux := http.NewServeMux()
	production := os.Getenv("APP_ENV") == "production" || os.Getenv("GO_ENV") == "production"
	snapshotSource := dashboard.SnapshotSource{DB: db}
	mux.Handle("/", &dashboard.Server{
		User: user, Password: os.Getenv("DASHBOARD_PASSWORD"), SessionSecret: sessionSecret,
		Production: production, Snapshot: snapshotSource.Build,
	})
	token := os.Getenv("DISCORD_BOT_TOKEN")
	interval := 30 * time.Second
	if seconds, parseErr := strconv.Atoi(os.Getenv("UADE_POLL_INTERVAL_SECONDS")); parseErr == nil && seconds > 0 {
		interval = time.Duration(seconds) * time.Second
	}
	runtime, runtimeErr := app.NewRuntime(context.Background(), db, os.Getenv("CREDENTIALS_MASTER_KEY"), token, ssoPortalURL, interval, 2, mode == "shadow")
	if runtimeErr != nil {
		log.Fatal(runtimeErr)
	}
	defer runtime.Close()
	runtime.Start()

	server := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}
	serverErr := make(chan error, 1)
	go func() {
		log.Printf("uade-go listening on %s", addr)
		serverErr <- server.ListenAndServe()
	}()

	if token != "" && mode != "shadow" {
		dispatcher := discordhttp.CommandDispatcher{DB: db, MasterKey: os.Getenv("CREDENTIALS_MASTER_KEY"), OnJobCreated: runtime.JobCreated, OnAccountReady: runtime.AccountReady, OnJobsChanged: runtime.JobsChanged}
		client, clientErr := discordgateway.New(token, dispatcher)
		if clientErr != nil {
			log.Fatal(clientErr)
		}
		if clientErr = client.OpenGateway(context.Background()); clientErr != nil {
			log.Fatal(clientErr)
		}
		defer client.Close(context.Background())
		log.Printf("discord gateway connected with interaction listeners")
		applicationID := os.Getenv("DISCORD_CLIENT_ID")
		if applicationID == "" {
			log.Fatal("DISCORD_CLIENT_ID is required when DISCORD_BOT_TOKEN is configured")
		}
		registerCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		registerErr := discordhttp.RegisterGlobal(registerCtx, nil, "", token, applicationID)
		cancel()
		if registerErr != nil {
			// Existing global commands remain usable; a transient REST outage must
			// not take presence or the interaction endpoint offline.
			log.Printf("global discord command registration failed: %v", registerErr)
		} else {
			log.Printf("registered 12 global discord commands")
		}
	}
	log.Fatal(<-serverErr)
}
