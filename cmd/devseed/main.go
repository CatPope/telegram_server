package main

import (
	"fmt"
	"os"

	"github.com/CatPope/telegram_server/internal/auth"
)

type devKey struct {
	appID   string
	prefix  string
	cleartext string
	label   string
}

func main() {
	keys := []devKey{
		{"dev-admin", "devadmin", "tg_devadmin_0123456789abcdef0123456789abcdef", "Local dev admin key"},
		{"dev-developer", "devdev", "tg_devdev_0123456789abcdef0123456789abcdef", "Local dev developer key"},
		{"dev-user", "devuser", "tg_devuser_0123456789abcdef0123456789abcdef", "Local dev user-grade key"},
	}
	for _, k := range keys {
		h, err := auth.HashAPIKey(k.cleartext)
		if err != nil {
			fmt.Fprintf(os.Stderr, "hash %s: %v\n", k.appID, err)
			os.Exit(1)
		}
		fmt.Printf("APP_ID=%s\nPREFIX=%s\nCLEARTEXT=%s\nHASH=%s\nLABEL=%s\n---\n",
			k.appID, k.prefix, k.cleartext, h, k.label)
	}
}
