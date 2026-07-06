package adminui

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// Config holds the admin UI's own settings, separate from cmd/server's
// Config — the UI runs as its own binary against a running server.
type Config struct {
	ListenAddr        string
	Password          string
	APIKey            string
	TelegramServerURL string
	// DatabaseURL is optional in Phase A2 — when unset, the apps list/
	// detail pages render a "DB not connected" notice instead of dialing,
	// and every other page keeps working (all mutations go through the
	// /admin API, not this connection).
	DatabaseURL string
	// CookieSecure marks session/CSRF cookies Secure. Off by default so
	// the loopback/사설망 http 배치가 그대로 동작하고, TLS 종단 뒤에 둘 때
	// ADMINUI_COOKIE_SECURE=true로 켠다.
	CookieSecure bool
}

const (
	envListenAddr   = "ADMINUI_LISTEN_ADDR"
	envPassword     = "ADMINUI_PASSWORD"
	envAPIKey       = "ADMINUI_API_KEY"
	envServerURL    = "TELEGRAM_SERVER_URL"
	envDatabaseURL  = "DATABASE_URL"
	envCookieSecure = "ADMINUI_COOKIE_SECURE"
)

func Load() (Config, error) {
	cfg := Config{
		ListenAddr:        strings.TrimSpace(os.Getenv(envListenAddr)),
		Password:          strings.TrimSpace(os.Getenv(envPassword)),
		APIKey:            strings.TrimSpace(os.Getenv(envAPIKey)),
		TelegramServerURL: strings.TrimSpace(os.Getenv(envServerURL)),
		DatabaseURL:       strings.TrimSpace(os.Getenv(envDatabaseURL)),
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv(envCookieSecure))) {
	case "1", "true", "yes":
		cfg.CookieSecure = true
	}
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = "127.0.0.1:8081"
	}
	if cfg.TelegramServerURL == "" {
		cfg.TelegramServerURL = "http://127.0.0.1:8080"
	}
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

var errMissingRequired = errors.New("adminui/config: missing required env var")

func (c Config) validate() error {
	if c.Password == "" {
		return fmt.Errorf("%w: %s", errMissingRequired, envPassword)
	}
	if c.APIKey == "" {
		return fmt.Errorf("%w: %s", errMissingRequired, envAPIKey)
	}
	return nil
}
