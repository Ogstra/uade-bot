package main

import (
	"context"
	"database/sql"
	"errors"
	"github.com/Ogstra/uade-bot/internal/app"
	"github.com/Ogstra/uade-bot/internal/dashboard"
	"github.com/Ogstra/uade-bot/internal/discordgateway"
	"github.com/Ogstra/uade-bot/internal/discordhttp"
	"github.com/Ogstra/uade-bot/internal/discordrest"
	"github.com/Ogstra/uade-bot/internal/store"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/events"
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

// checkRuntimeMode accepts the values a working deployment can carry and
// rejects the one that no longer exists. Shadow mode (read-only database, no
// Discord) only made sense next to the Node implementation it was compared
// against; with that gone, a box still configured for shadow is refused
// loudly instead of quietly coming up fully active against a database its
// operator expected to stay untouched.
func checkRuntimeMode(raw string) error {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "active", "development":
		return nil
	case "shadow":
		return errors.New("UADE_RUNTIME_MODE=shadow ya no existe: el modo de comparación contra Node se eliminó. Sacá la variable o ponela en active")
	default:
		return errors.New("UADE_RUNTIME_MODE debe ser active (o quedar vacía)")
	}
}

func main() {
	path := os.Getenv("UADE_DB_PATH")
	if path == "" {
		path = "data/uade.db"
	}
	if err := checkRuntimeMode(os.Getenv("UADE_RUNTIME_MODE")); err != nil {
		log.Fatal(err)
	}
	ssoPortalURL, ssoErr := resolveSSOPortalURL(os.Getenv("UADE_SSO_PORTAL_URL"))
	if ssoErr != nil {
		log.Fatal(ssoErr)
	}
	db, err := store.Open(path)
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
	gatewayProjection := discordgateway.NewProjection(nil)
	snapshotSource := dashboardSnapshotSource(db, path, gatewayProjection)
	dashboardServer := &dashboard.Server{
		User: user, Password: os.Getenv("DASHBOARD_PASSWORD"), SessionSecret: sessionSecret,
		Production: production, Snapshot: snapshotSource.Build, DB: db,
	}
	mux.Handle("/", dashboardServer)
	token := os.Getenv("DISCORD_BOT_TOKEN")
	interval := 30 * time.Second
	if seconds, parseErr := strconv.Atoi(os.Getenv("UADE_POLL_INTERVAL_SECONDS")); parseErr == nil && seconds > 0 {
		interval = time.Duration(seconds) * time.Second
	}
	runtime, runtimeErr := app.NewRuntime(context.Background(), db, os.Getenv("CREDENTIALS_MASTER_KEY"), token, ssoPortalURL, interval, 2)
	if runtimeErr != nil {
		log.Fatal(runtimeErr)
	}
	defer runtime.Close()
	runtime.Start()

	// Wired here (not in the dashboardServer literal above) because runtime
	// only exists after runtime.Start(); these are the same two callbacks
	// already wired to discordhttp.CommandDispatcher below, reused as-is so
	// a dashboard mutation reconciles the scheduler exactly like the
	// equivalent Discord admin-* command.
	dashboardServer.OnJobsChanged = runtime.JobsChanged
	dashboardServer.OnJobCreated = runtime.JobCreated

	server := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}
	serverErr := make(chan error, 1)
	go func() {
		log.Printf("uade-go listening on %s", addr)
		serverErr <- server.ListenAndServe()
	}()

	if token != "" {
		applicationID := os.Getenv("DISCORD_CLIENT_ID")
		if applicationID == "" {
			log.Fatal("DISCORD_CLIENT_ID is required when DISCORD_BOT_TOKEN is configured")
		}
		dispatcher := discordhttp.CommandDispatcher{
			DB: db, MasterKey: os.Getenv("CREDENTIALS_MASTER_KEY"),
			SuperAdminID: os.Getenv("UADE_SUPER_ADMIN_ID"),
			OnJobCreated: runtime.JobCreated, PrepareAccount: runtime.PrepareAccount,
			OnAccountActivated: runtime.AccountActivated, OnJobsChanged: runtime.JobsChanged,
			ResolveMateria: runtime.ResolveMateria,
			DBPath:         path,
		}
		dispatcher = wireGatewayIdentity(dispatcher, gatewayProjection)
		notifier := discordrest.New(token)
		dispatcher.SendChannel = func(channel, content string) error { return notifier.Send(channel, content) }
		if microsoft := entraFromEnv(path, func(user string, card discordrest.Card, text string) {
			if err := notifier.SendDMCard(user, card, text, ""); err != nil {
				log.Printf("entra: could not deliver sign-in notification")
			}
		}); microsoft != nil {
			dispatcher.Microsoft = microsoft
			go microsoft.Run(context.Background())
		}
		client, clientErr := discordgateway.New(token, dispatcher, gatewayProjection)
		if clientErr != nil {
			log.Fatal(clientErr)
		}
		// OpenGateway returning does not guarantee the guild cache is populated.
		// disgo's GuildsReady event is emitted only after every initial guild has
		// loaded, making the first normal boot cleanup deterministic. Reconnects
		// safely repeat the same idempotent convergence.
		client.AddEventListeners(bot.NewListenerFunc(func(*events.GuildsReady) {
			go func() {
				guilds := gatewayProjection.Guilds()
				guildIDs := make([]string, 0, len(guilds))
				for _, guild := range guilds {
					guildIDs = append(guildIDs, guild.ID)
				}
				registerCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				if err := discordhttp.ConvergeCommands(registerCtx, nil, "", token, applicationID, guildIDs); err != nil {
					log.Printf("discord command convergence failed: %v", err)
					return
				}
				log.Printf("converged %d global discord commands and cleared guild-scoped copies in %d guilds", len(discordhttp.GlobalCommands()), len(guildIDs))
			}()
		}))
		if clientErr = client.OpenGateway(context.Background()); clientErr != nil {
			log.Fatal(clientErr)
		}
		defer client.Close(context.Background())
		log.Printf("discord gateway connected with interaction listeners")
	}
	log.Fatal(<-serverErr)
}

func wireGatewayIdentity(dispatcher discordhttp.CommandDispatcher, projection *discordgateway.Projection) discordhttp.CommandDispatcher {
	dispatcher.IdentityResolver = projection
	return dispatcher
}

func dashboardSnapshotSource(db *sql.DB, dbPath string, projection *discordgateway.Projection) dashboard.SnapshotSource {
	source := dashboard.SnapshotSource{DB: db, DBPath: dbPath}
	if projection != nil {
		source.GuildProvider = projection.Guilds
		source.DisplayNameProvider = projection.DisplayNames
		source.AvatarURLProvider = projection.AvatarURLs
	}
	return source
}
