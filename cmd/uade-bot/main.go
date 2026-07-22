package main

import (
	"context"
	"github.com/disgoorg/disgo"
	"github.com/disgoorg/disgo/bot"
	"github.com/ogs/uade-bot/internal/dashboard"
	"github.com/ogs/uade-bot/internal/store"
	"log"
	"net/http"
	"os"
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
	if token := os.Getenv("DISCORD_BOT_TOKEN"); token != "" {
		client, err := disgo.New(token, bot.WithDefaultGateway())
		if err != nil {
			log.Fatal(err)
		}
		if err := client.OpenGateway(context.Background()); err != nil {
			log.Fatal(err)
		}
		defer client.Close(context.Background())
		log.Printf("discord gateway connected")
	}
	addr := os.Getenv("DASHBOARD_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	log.Printf("uade-go listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, dashboard.Server{User: os.Getenv("DASHBOARD_USER"), Password: os.Getenv("DASHBOARD_PASSWORD")}))
}
