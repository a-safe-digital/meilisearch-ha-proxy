package health

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/a-safe-digital/meilisearch-ha-proxy/internal/config"
)

// Checker periodically health-checks all MeiliSearch nodes.
type Checker struct {
	nodes    []*Node
	interval time.Duration
	timeout  time.Duration
}

// NewChecker creates a Checker from configuration.
func NewChecker(cfg *config.Config) *Checker {
	nodes := make([]*Node, len(cfg.Nodes))
	for i, nc := range cfg.Nodes {
		nodes[i] = NewNode(
			nc.URL,
			nc.APIKey,
			nc.Role,
			cfg.HealthCheck.UnhealthyThreshold,
			cfg.HealthCheck.HealthyThreshold,
		)
	}
	return &Checker{
		nodes:    nodes,
		interval: cfg.HealthCheck.Interval,
		timeout:  cfg.HealthCheck.Timeout,
	}
}

// Nodes returns all tracked nodes.
func (c *Checker) Nodes() []*Node {
	return c.nodes
}

// Primary returns the current primary node, or nil if none.
func (c *Checker) Primary() *Node {
	for _, n := range c.nodes {
		if n.Role == "primary" {
			return n
		}
	}
	return nil
}

// HealthyReplicas returns all replica nodes in Healthy state.
func (c *Checker) HealthyReplicas() []*Node {
	var result []*Node
	for _, n := range c.nodes {
		if n.Role == "replica" && n.IsHealthy() {
			result = append(result, n)
		}
	}
	return result
}

// HealthyNodes returns all nodes in Healthy state (primary + replicas).
func (c *Checker) HealthyNodes() []*Node {
	var result []*Node
	for _, n := range c.nodes {
		if n.IsHealthy() {
			result = append(result, n)
		}
	}
	return result
}

// Run starts the periodic health check loop. It blocks until ctx is cancelled.
func (c *Checker) Run(ctx context.Context) {
	slog.Info("health checker started",
		"nodes", len(c.nodes),
		"interval", c.interval,
	)

	// Initial check
	c.checkAll(ctx)

	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("health checker stopped")
			return
		case <-ticker.C:
			c.checkAll(ctx)
		}
	}
}

func (c *Checker) checkAll(ctx context.Context) {
	var wg sync.WaitGroup
	for _, node := range c.nodes {
		wg.Add(1)
		go func(n *Node) {
			defer wg.Done()
			n.Check(ctx, c.timeout)
		}(node)
	}
	wg.Wait()
}
