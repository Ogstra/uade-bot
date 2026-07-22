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
	token := os.Getenv("DISCORD_BOT_TOKEN")
	if token != "" {
		client, err := disgo.New(token, bot.WithDefaultGateway())
		if err != nil {
			log.Fatal(err)
		}
		if err := client.OpenGateway(context.Background()); err != nil {
			log.Fatal(err)
		}
		defer client.Close(context.Background())
		log.Printf("discord gateway connected")
		applicationID := os.Getenv("DISCORD_CLIENT_ID")
		if applicationID == "" {
			log.Fatal("DISCORD_CLIENT_ID is required when DISCORD_BOT_TOKEN is configured")
		}
		registerCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		err = discordhttp.RegisterGlobal(registerCtx, nil, "", token, applicationID)
		cancel()
		if err != nil {
			log.Fatalf("register global discord commands: %v", err)
		}
		log.Printf("registered 12 global discord commands")
	}
	addr := os.Getenv("DASHBOARD_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	user := os.Getenv("DASHBOARD_USERNAME")
	if user == "" {
		user = os.Getenv("DASHBOARD_USER")
	}
	mux := http.NewServeMux()
	mux.Handle("/", dashboard.Server{User: user, Password: os.Getenv("DASHBOARD_PASSWORD")})
	publicKeyHex := os.Getenv("DISCORD_PUBLIC_KEY")
	if publicKeyHex != "" {
		publicKey, decodeErr := hex.DecodeString(publicKeyHex)
		if decodeErr != nil || len(publicKey) != ed25519.PublicKeySize {
			log.Fatal("DISCORD_PUBLIC_KEY must be a 32-byte hex key")
		}
		mux.Handle("/discord/interactions", &discordhttp.Handler{PublicKey: ed25519.PublicKey(publicKey), Dispatch: discordhttp.CommandDispatcher{DB: db, MasterKey: os.Getenv("CREDENTIALS_MASTER_KEY")}})
		log.Printf("discord HTTP interactions enabled")
	}
	log.Printf("uade-go listening on %s", addr)
	server := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}
	log.Fatal(server.ListenAndServe())
}
