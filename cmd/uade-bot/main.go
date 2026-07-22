package main

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"github.com/disgoorg/disgo"
	"github.com/disgoorg/disgo/bot"
	"github.com/ogs/uade-bot/internal/dashboard"
	"github.com/ogs/uade-bot/internal/discordhttp"
	"github.com/ogs/uade-bot/internal/store"
	"log"
	"net/http"
	"os"
	"time"
)

func main() {
	path := os.Getenv("UADE_DB_PATH")
	if path == "" {
		path = "data/uade.db"
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
	mux.Handle("/", &dashboard.Server{User: user, Password: os.Getenv("DASHBOARD_PASSWORD"), SessionSecret: sessionSecret})
	publicKeyHex := os.Getenv("DISCORD_PUBLIC_KEY")
	if publicKeyHex != "" {
		publicKey, decodeErr := hex.DecodeString(publicKeyHex)
		if decodeErr != nil || len(publicKey) != ed25519.PublicKeySize {
			log.Fatal("DISCORD_PUBLIC_KEY must be a 32-byte hex key")
		}
		mux.Handle("/discord/interactions", &discordhttp.Handler{PublicKey: ed25519.PublicKey(publicKey), Dispatch: discordhttp.CommandDispatcher{DB: db, MasterKey: os.Getenv("CREDENTIALS_MASTER_KEY")}})
		log.Printf("discord HTTP interactions enabled")
	}
	server := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}
	serverErr := make(chan error, 1)
	go func() {
		log.Printf("uade-go listening on %s", addr)
		serverErr <- server.ListenAndServe()
	}()

	token := os.Getenv("DISCORD_BOT_TOKEN")
	if token != "" {
		client, clientErr := disgo.New(token, bot.WithDefaultGateway())
		if clientErr != nil {
			log.Fatal(clientErr)
		}
		if clientErr = client.OpenGateway(context.Background()); clientErr != nil {
			log.Fatal(clientErr)
		}
		defer client.Close(context.Background())
		log.Printf("discord gateway connected")
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
