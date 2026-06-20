package main

import (
	"log"
	"os"

	"github.com/mymmrac/telego"
)

func main() {
	token := os.Getenv("TELEGRAM_BOT_TOKEN")
	if token == "" {
		log.Fatal("TELEGRAM_BOT_TOKEN is not set")
	}

	bot, err := telego.NewBot(token)
	if err != nil {
		log.Fatalf("create bot: %v", err)
	}
	_ = bot

	log.Println("telegram_server initialized")
}
