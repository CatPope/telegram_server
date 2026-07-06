package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/CatPope/telegram_server/internal/adminui"
	"github.com/CatPope/telegram_server/internal/api/middleware"
	"github.com/CatPope/telegram_server/internal/audit"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

// run wraps the real main body so deferred cleanup always executes, same
// pattern as cmd/server/main.go.
func run() error {
	cfg, err := adminui.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// DATABASE_URL is optional — the apps pages need it for read-only
	// queries (no "list apps" API exists) and the keys pages for the one
	// direct write (app_keys issue/revoke, Phase A3), but the UI starts
	// fine without it and shows a "DB not connected" notice.
	var store adminui.Store
	var keys adminui.KeyStore
	var auditW audit.Writer
	if cfg.DatabaseURL != "" {
		pool, poolErr := pgxpool.New(ctx, cfg.DatabaseURL)
		if poolErr != nil {
			return fmt.Errorf("db: %w", poolErr)
		}
		defer pool.Close()
		store = adminui.NewStore(pool)
		keys = adminui.NewKeyStore(pool)
		auditW = audit.NewPgWriter(pool)
	}

	handler, err := adminui.NewServer(cfg, store, keys, auditW)
	if err != nil {
		return fmt.Errorf("server: %w", err)
	}

	if !cfg.CookieSecure && !cfg.ListensLoopbackOnly() {
		middleware.Log("warn", "adminui_insecure_cookies_on_public_addr", map[string]any{
			"addr": cfg.ListenAddr,
			"hint": "set ADMINUI_COOKIE_SECURE=true behind TLS, or bind to 127.0.0.1",
		})
	}

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	middleware.Log("info", "adminui_starting", map[string]any{
		"addr":          cfg.ListenAddr,
		"target_server": cfg.TelegramServerURL,
	})

	errCh := make(chan error, 1)
	go func() {
		if listenErr := srv.ListenAndServe(); listenErr != nil && !errors.Is(listenErr, http.ErrServerClosed) {
			errCh <- listenErr
		}
		close(errCh)
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	select {
	case <-sigCh:
		middleware.Log("info", "adminui_shutdown_requested", nil)
	case listenErr := <-errCh:
		if listenErr != nil {
			return fmt.Errorf("listen: %w", listenErr)
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if shutdownErr := srv.Shutdown(shutdownCtx); shutdownErr != nil {
		middleware.Log("error", "adminui_shutdown_failed", map[string]any{"error": shutdownErr.Error()})
	}
	middleware.Log("info", "adminui_stopped", nil)
	return nil
}
