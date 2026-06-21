package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

type Config struct {
	TelegramBotToken    string
	TelegramBotUsername string
	TelegramAPIURL      string // optional; redirects telego to a mock server for dev/test
	DatabaseURL         string
	HTTPListenAddr      string
	LogLevel            string
}

const (
	envBotToken    = "TELEGRAM_BOT_TOKEN"
	envBotUsername = "TELEGRAM_BOT_USERNAME"
	envAPIURL      = "TELEGRAM_API_URL"
	envDatabaseURL = "DATABASE_URL"
	envHTTPListen  = "HTTP_LISTEN_ADDR"
	envLogLevel    = "LOG_LEVEL"
)

func Load() (Config, error) {
	cfg := Config{
		TelegramBotToken:    strings.TrimSpace(os.Getenv(envBotToken)),
		TelegramBotUsername: strings.TrimSpace(os.Getenv(envBotUsername)),
		TelegramAPIURL:      strings.TrimSpace(os.Getenv(envAPIURL)),
		DatabaseURL:         strings.TrimSpace(os.Getenv(envDatabaseURL)),
		HTTPListenAddr:      strings.TrimSpace(os.Getenv(envHTTPListen)),
		LogLevel:            strings.TrimSpace(os.Getenv(envLogLevel)),
	}
	if cfg.HTTPListenAddr == "" {
		cfg.HTTPListenAddr = "127.0.0.1:8080"
	}
	if cfg.LogLevel == "" {
		cfg.LogLevel = "info"
	}
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

var errMissingRequired = errors.New("config: missing required env var")

func (c Config) validate() error {
	if c.TelegramBotToken == "" {
		return fmt.Errorf("%w: %s", errMissingRequired, envBotToken)
	}
	if c.TelegramBotUsername == "" {
		return fmt.Errorf("%w: %s", errMissingRequired, envBotUsername)
	}
	if c.DatabaseURL == "" {
		return fmt.Errorf("%w: %s", errMissingRequired, envDatabaseURL)
	}
	return nil
}
