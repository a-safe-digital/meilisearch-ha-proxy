package health

import (
	"log/slog"
	"sync"
)

// ReplicaLagProvider returns the last replicated taskUID per replica URL.
type ReplicaLagProvider interface {
	ReplicaLag() map[string]int64
}

// FailoverManager handles automatic primary failover and recovery.
type FailoverManager struct {
	checker   *Checker
	lagSource ReplicaLagProvider

	mu         sync.Mutex
	failedOver bool // true if a replica has been promoted
}

// NewFailoverManager creates a failover manager.
func NewFailoverManager(checker *Checker, lagSource ReplicaLagProvider) *FailoverManager {
	return &FailoverManager{
		checker:   checker,
		lagSource: lagSource,
	}
}

// Evaluate checks for failover conditions and performs promotion/demotion.
// Should be called after each health check round.
func (f *FailoverManager) Evaluate() {
	f.mu.Lock()
	defer f.mu.Unlock()

	primary := f.checker.Primary()
	if primary == nil {
		// No primary at all — try to promote
		f.tryPromote()
		return
	}

	if primary.IsHealthy() {
		// Primary is healthy — check if we need to recover from a previous failover
		if f.failedOver {
			f.tryRecover(primary)
		}
		return
	}

	// Primary is unhealthy — trigger failover
	f.tryPromote()
}

// IsFailedOver returns true if a failover has occurred (a replica is acting as primary).
func (f *FailoverManager) IsFailedOver() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.failedOver
}

func (f *FailoverManager) tryPromote() {
	// Find the best replica to promote: healthy replica with highest replicated taskUID
	var bestNode *Node
	var bestTaskUID int64

	var lag map[string]int64
	if f.lagSource != nil {
		lag = f.lagSource.ReplicaLag()
	}

	for _, n := range f.checker.Nodes() {
		if n.GetRole() != "replica" || !n.IsHealthy() {
			continue
		}

		taskUID := int64(0)
		if lag != nil {
			taskUID = lag[n.URL]
		}

		if bestNode == nil || taskUID > bestTaskUID {
			bestNode = n
			bestTaskUID = taskUID
		}
	}

	if bestNode == nil {
		slog.Error("failover: no healthy replica available for promotion")
		return
	}

	// Demote current primary (if any)
	current := f.checker.Primary()
	if current != nil {
		slog.Warn("failover: demoting primary",
			"url", current.URL,
		)
		current.SetRole("replica")
	}

	// Promote the best replica
	slog.Warn("failover: promoting replica to primary",
		"url", bestNode.URL,
		"lastTaskUid", bestTaskUID,
	)
	bestNode.SetRole("primary")
	f.failedOver = true
}

func (f *FailoverManager) tryRecover(currentPrimary *Node) {
	// Check if the original primary has recovered
	// The original primary is the node whose originalRole is "primary"
	// but is currently a "replica" (was demoted during failover)
	var originalPrimary *Node
	for _, n := range f.checker.Nodes() {
		if n.OriginalRole() == "primary" && n.GetRole() == "replica" {
			originalPrimary = n
			break
		}
	}

	if originalPrimary == nil || !originalPrimary.IsHealthy() {
		// Original primary hasn't recovered yet
		return
	}

	// Original primary is healthy again — restore original roles
	slog.Info("failover: original primary recovered, restoring roles",
		"originalPrimary", originalPrimary.URL,
		"currentPrimary", currentPrimary.URL,
	)

	currentPrimary.SetRole("replica")
	originalPrimary.SetRole("primary")
	f.failedOver = false
}
