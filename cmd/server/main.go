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
	botpkg "github.com/CatPope/telegram_server/internal/bot"
	bothandlers "github.com/CatPope/telegram_server/internal/bot/handlers"
	"github.com/CatPope/telegram_server/internal/config"
	"github.com/CatPope/telegram_server/internal/dispatch/strategy"
	tgdisp "github.com/CatPope/telegram_server/internal/dispatch/telegram"
	"github.com/CatPope/telegram_server/internal/ratelimit"
	"github.com/CatPope/telegram_server/internal/registry"
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

	botOpts := []telego.BotOption{}
	if cfg.TelegramAPIURL != "" {
		botOpts = append(botOpts, telego.WithAPIServer(cfg.TelegramAPIURL))
	}
	bot, err := telego.NewBot(cfg.TelegramBotToken, botOpts...)
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

	users := registry.NewUserStore(pool)
	supergroups := registry.NewSupergroupStore(pool)
	startHandler := &bothandlers.StartHandler{
		Bot:         bot,
		BotUsername: cfg.TelegramBotUsername,
		Users:       users,
		Supergroups: supergroups,
		Audit:       auditW,
	}
	poller := botpkg.NewPoller(bot, startHandler)

	botCtx, botCancel := context.WithCancel(ctx)
	defer botCancel()
	botDone := make(chan struct{})
	go func() {
		if pErr := poller.Run(botCtx); pErr != nil {
			middleware.Log("error", "bot_poller_run", map[string]any{"error": pErr.Error()})
		}
		close(botDone)
	}()

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

	// Tear down bot poller first so in-flight updates can finish their
	// audit writes against the still-open DB pool; SIGTERM context
	// cancel propagates to telego's UpdatesViaLongPolling per Pre-mortem #4.
	botCancel()
	select {
	case <-botDone:
	case <-time.After(10 * time.Second):
		middleware.Log("warn", "bot_poller_drain_timeout", nil)
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
