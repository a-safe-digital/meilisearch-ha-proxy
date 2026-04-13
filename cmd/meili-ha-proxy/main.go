package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/a-safe-digital/meilisearch-ha-proxy/internal/config"
	"github.com/a-safe-digital/meilisearch-ha-proxy/internal/health"
	"github.com/a-safe-digital/meilisearch-ha-proxy/internal/metrics"
	"github.com/a-safe-digital/meilisearch-ha-proxy/internal/proxy"
	"github.com/a-safe-digital/meilisearch-ha-proxy/internal/replication"
)

var version = "dev"

func main() {
	os.Exit(run())
}

func run() int {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	configPath := "configs/meili-ha.yaml"
	if v := os.Getenv("MEILI_HA_CONFIG"); v != "" {
		configPath = v
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		slog.Error("failed to load config", "error", err)
		return 1
	}

	// Apply log level from config
	configureLogging(cfg.LogLevel)

	slog.Info("meilisearch-ha-proxy starting",
		"version", version,
		"listen", cfg.Listen,
		"metrics_listen", cfg.MetricsListen,
		"nodes", len(cfg.Nodes),
	)

	// Initialize health checker
	checker := health.NewChecker(cfg)

	// Initialize replicator
	repTimeout := 30 * time.Second
	if cfg.Replication.Timeout > 0 {
		repTimeout = cfg.Replication.Timeout
	}
	replicator := replication.New(checker, repTimeout)

	// Initialize failover manager
	fm := health.NewFailoverManager(checker, replicator)
	checker.SetFailoverManager(fm)

	// Start health checker in background
	go checker.Run(ctx)

	// Initialize proxy with MaxPayloadSize
	maxPayload := config.ParseSize(cfg.Replication.MaxPayloadSize)
	p := proxy.New(checker, proxy.WithMaxPayloadSize(maxPayload))
	p.SetReplicator(replicator)
	p.SetAdminHandler(proxy.NewAdminHandler(checker, replicator))

	// Start metrics server
	metricsSrv := &http.Server{
		Addr:              cfg.MetricsListen,
		Handler:           metrics.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	go func() {
		slog.Info("metrics server listening", "addr", cfg.MetricsListen)
		if err := metricsSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("metrics server error", "error", err)
		}
	}()

	// Start main HTTP server with timeouts to prevent slowloris and connection exhaustion
	server := &http.Server{
		Addr:              cfg.Listen,
		Handler:           p,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      120 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("listening", "addr", cfg.Listen)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		fmt.Println() // clean line after ^C
		slog.Info("shutting down")
	case err := <-errCh:
		slog.Error("server error", "error", err)
		cancel()
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	// Shutdown both servers
	if err := metricsSrv.Shutdown(shutdownCtx); err != nil {
		slog.Error("metrics shutdown", "error", err)
	}
	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Error("shutdown", "error", err)
	}
	return 0
}

func configureLogging(level string) {
	var logLevel slog.Level
	switch level {
	case "debug":
		logLevel = slog.LevelDebug
	case "warn", "warning":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	default:
		logLevel = slog.LevelInfo
	}

	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: logLevel,
	})
	slog.SetDefault(slog.New(handler))
}
