// mocktelegram standalone binary: a long-running Bot API stub, intended as
// a docker-compose sidecar for local end-to-end runs. It listens on the
// MOCKTELEGRAM_ADDR address (default :8090) and reuses the mocktelegram
// package handler. There is no record-replay; restart the process to drop
// inbound history.
package main

import (
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/CatPope/telegram_server/internal/mocktelegram"
)

func main() {
	addr := strings.TrimSpace(os.Getenv("MOCKTELEGRAM_ADDR"))
	if addr == "" {
		addr = ":8090"
	}
	srv := mocktelegram.NewHandler()
	log.Printf("mocktelegram listening on %s", addr)
	if err := http.ListenAndServe(addr, srv); err != nil { //nolint:gosec
		log.Fatalf("mocktelegram: %v", err)
	}
}
