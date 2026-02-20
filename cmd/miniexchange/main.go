package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/efreitasn/miniexchange/internal/config"
	"github.com/efreitasn/miniexchange/internal/domain"
	"github.com/efreitasn/miniexchange/internal/engine"
	"github.com/efreitasn/miniexchange/internal/handler"
	"github.com/efreitasn/miniexchange/internal/service"
	"github.com/efreitasn/miniexchange/internal/store"
)

func main() {
	healthcheck := flag.Bool("healthcheck", false, "Run health check against running server")
	flag.Parse()

	// Handle -healthcheck flag: HTTP GET to localhost:PORT/healthz, exit 0/1.
	if *healthcheck {
		port := os.Getenv("PORT")
		if port == "" {
			port = "8080"
		}
		resp, err := http.Get(fmt.Sprintf("http://localhost:%s/healthz", port))
		if err != nil || resp.StatusCode != http.StatusOK {
			os.Exit(1)
		}
		os.Exit(0)
	}

	// Load configuration.
	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", slog.String("error", err.Error()))
		os.Exit(1)
	}

	// Set up slog logger with configured level.
	var logLevel slog.Level
	switch cfg.LogLevel {
	case "debug":
		logLevel = slog.LevelDebug
	case "warn":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	default:
		logLevel = slog.LevelInfo
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: logLevel,
	}))
	slog.SetDefault(logger)

	// Instantiate stores.
	brokerStore := store.NewBrokerStore()
	orderStore := store.NewOrderStore()
	tradeStore := store.NewTradeStore()
	webhookStore := store.NewWebhookStore()

	// Domain.
	symbols := domain.NewSymbolRegistry()

	// Engine.
	books := engine.NewBookManager()
	matcher := engine.NewMatcher(books, brokerStore, orderStore, tradeStore, symbols)

	// Services (webhook first — needed by expiry manager).
	webhookSvc := service.NewWebhookService(webhookStore, brokerStore, cfg.WebhookTimeout)
	brokerSvc := service.NewBrokerService(brokerStore, symbols)

	// Expiry manager (depends on webhook service as dispatcher).
	expiryMgr := engine.NewExpiryManager(
		cfg.ExpirationInterval,
		books,
		orderStore,
		brokerStore,
		webhookSvc,
	)

	orderSvc := service.NewOrderService(matcher, expiryMgr, brokerStore, orderStore, tradeStore, webhookSvc, symbols)
	stockSvc := service.NewStockService(tradeStore, books, matcher, cfg.VWAPWindow, symbols)

	// Router.
	router := handler.NewRouter(brokerSvc, orderSvc, stockSvc, webhookSvc, logger)

	// Start expiration goroutine with cancellable context.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go expiryMgr.Start(ctx)

	// Configure HTTP server.
	addr := fmt.Sprintf(":%d", cfg.Port)
	srv := &http.Server{
		Addr:         addr,
		Handler:      router,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		IdleTimeout:  cfg.IdleTimeout,
	}

	// Start HTTP server in a goroutine.
	go func() {
		logger.Info("server starting", slog.String("addr", addr))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("server error", slog.String("error", err.Error()))
			os.Exit(1)
		}
	}()

	// Wait for SIGINT/SIGTERM.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh
	logger.Info("shutdown signal received", slog.String("signal", sig.String()))

	// Graceful shutdown: stop HTTP server, cancel context (stops expiry goroutine).
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("server shutdown error", slog.String("error", err.Error()))
	}
	cancel()

	logger.Info("server stopped")
}
