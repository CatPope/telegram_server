package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mymmrac/telego"

	"github.com/CatPope/telegram_server/internal/api"
	"github.com/CatPope/telegram_server/internal/api/middleware"
	"github.com/CatPope/telegram_server/internal/audit"
	"github.com/CatPope/telegram_server/internal/auth"
	"github.com/CatPope/telegram_server/internal/config"
	"github.com/CatPope/telegram_server/internal/dispatch/strategy"
	tgdisp "github.com/CatPope/telegram_server/internal/dispatch/telegram"
	"github.com/CatPope/telegram_server/internal/ratelimit"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	defer pool.Close()

	if pingErr := waitForDB(ctx, pool, 30*time.Second); pingErr != nil {
		log.Fatalf("db wait: %v", pingErr)
	}

	auditW := audit.NewPgWriter(pool)
	keyStore := auth.NewKeyStore(pool)
	reqLimit := ratelimit.NewRequestLimiter(ratelimit.Policy{RatePerSec: 100, Burst: 100}, nil)

	bot, err := telego.NewBot(cfg.TelegramBotToken)
	if err != nil {
		log.Fatalf("telego: %v", err)
	}
	dispatcher := tgdisp.New(bot, tgdisp.NewDispatchLimiter())
	directStrategy := &strategy.DirectStrategy{Resolver: strategy.NewPgDirectResolver(pool)}
	topicStrategy := &strategy.TopicStrategy{Resolver: strategy.NewPgTopicResolver(pool)}
	broadcastStrategy := &strategy.BroadcastAllStrategy{Resolver: strategy.NewPgBroadcastResolver(pool)}
	directDMStrategy := &strategy.DirectDMStrategy{Resolver: strategy.NewPgDirectDMResolver(pool)}

	router := api.NewRouter(api.Deps{
		Pool:       pool,
		Audit:      auditW,
		Resolver:   keyStore,
		ReqLimit:   reqLimit,
		Direct:     directStrategy,
		Topic:      topicStrategy,
		Broadcast:  broadcastStrategy,
		DirectDM:   directDMStrategy,
		Dispatcher: dispatcher,
	})

	srv := &http.Server{
		Addr:              cfg.HTTPListenAddr,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	middleware.Log("info", "server_starting", map[string]any{
		"addr":         cfg.HTTPListenAddr,
		"bot_username": cfg.TelegramBotUsername,
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
		middleware.Log("info", "shutdown_requested", nil)
	case listenErr := <-errCh:
		if listenErr != nil {
			log.Fatalf("listen: %v", listenErr)
		}
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if shutdownErr := srv.Shutdown(shutdownCtx); shutdownErr != nil {
		middleware.Log("error", "shutdown_failed", map[string]any{"error": shutdownErr.Error()})
	}
	middleware.Log("info", "server_stopped", nil)
}

func waitForDB(ctx context.Context, pool *pgxpool.Pool, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		err := pool.Ping(pingCtx)
		cancel()
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}
