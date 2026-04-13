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
	"github.com/a-safe-digital/meilisearch-ha-proxy/internal/proxy"
	"github.com/a-safe-digital/meilisearch-ha-proxy/internal/replication"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	configPath := "configs/meili-ha.yaml"
	if v := os.Getenv("MEILI_HA_CONFIG"); v != "" {
		configPath = v
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	slog.Info("meilisearch-ha-proxy starting",
		"listen", cfg.Listen,
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

	// Initialize proxy
	p := proxy.New(checker)
	p.SetReplicator(replicator)
	p.SetAdminHandler(proxy.NewAdminHandler(checker, replicator))

	// Start HTTP server
	server := &http.Server{
		Addr:    cfg.Listen,
		Handler: p,
	}

	go func() {
		slog.Info("listening", "addr", cfg.Listen)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	fmt.Println() // clean line after ^C
	slog.Info("shutting down")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Error("shutdown", "error", err)
	}
}
