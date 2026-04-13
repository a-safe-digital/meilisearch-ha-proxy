package health

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// Node tracks the health state of a single MeiliSearch backend.
type Node struct {
	URL    string
	APIKey string
	Role   string // "primary" or "replica"

	mu                 sync.RWMutex
	state              State
	originalRole       string // role from config, used during failover recovery
	consecutiveFails   int
	consecutiveSuccess int
	lastCheck          time.Time
	lastErr            error

	unhealthyThreshold int
	healthyThreshold   int
}

// NewNode creates a Node with the given thresholds.
func NewNode(url, apiKey, role string, unhealthyThreshold, healthyThreshold int) *Node {
	return &Node{
		URL:                url,
		APIKey:             apiKey,
		Role:               role,
		originalRole:       role,
		state:              Healthy,
		unhealthyThreshold: unhealthyThreshold,
		healthyThreshold:   healthyThreshold,
	}
}

// SetRole changes the node's role (used during failover).
func (n *Node) SetRole(role string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.Role = role
}

// OriginalRole returns the role from the initial configuration.
func (n *Node) OriginalRole() string {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.originalRole
}

// State returns the current health state.
func (n *Node) State() State {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.state
}

// IsHealthy returns true if the node is in Healthy state.
func (n *Node) IsHealthy() bool {
	return n.State() == Healthy
}

// LastCheck returns the time of the last health check.
func (n *Node) LastCheck() time.Time {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.lastCheck
}

// LastError returns the error from the last failed health check, if any.
func (n *Node) LastError() error {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.lastErr
}

// Check performs a health check against the node's /health endpoint.
func (n *Node) Check(ctx context.Context, timeout time.Duration) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, n.URL+"/health", nil)
	if err != nil {
		n.recordFailure(fmt.Errorf("create request: %w", err))
		return
	}
	if n.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+n.APIKey)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		n.recordFailure(fmt.Errorf("health check: %w", err))
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		n.recordFailure(fmt.Errorf("health check: status %d", resp.StatusCode))
		return
	}

	n.recordSuccess()
}

func (n *Node) recordFailure(err error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	n.lastCheck = time.Now()
	n.lastErr = err
	n.consecutiveFails++
	n.consecutiveSuccess = 0

	prev := n.state

	switch n.state {
	case Healthy:
		n.state = Suspect
	case Suspect:
		if n.consecutiveFails >= n.unhealthyThreshold {
			n.state = Unhealthy
		}
	case Unhealthy:
		// stay unhealthy
	}

	if n.state != prev {
		slog.Warn("node state changed",
			"url", n.URL,
			"from", prev.String(),
			"to", n.state.String(),
			"error", err,
		)
	}
}

func (n *Node) recordSuccess() {
	n.mu.Lock()
	defer n.mu.Unlock()

	n.lastCheck = time.Now()
	n.lastErr = nil
	n.consecutiveSuccess++
	n.consecutiveFails = 0

	prev := n.state

	switch n.state {
	case Healthy:
		// stay healthy
	case Suspect:
		n.state = Healthy
	case Unhealthy:
		if n.consecutiveSuccess >= n.healthyThreshold {
			n.state = Healthy
		} else {
			n.state = Suspect
		}
	}

	if n.state != prev {
		slog.Info("node state changed",
			"url", n.URL,
			"from", prev.String(),
			"to", n.state.String(),
		)
	}
}
