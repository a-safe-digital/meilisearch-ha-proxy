package health

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/a-safe-digital/meilisearch-ha-proxy/internal/config"
)

// mockLagProvider implements ReplicaLagProvider for testing.
type mockLagProvider struct {
	lag map[string]int64
}

func (m *mockLagProvider) ReplicaLag() map[string]int64 {
	return m.lag
}

func makeUnhealthy(n *Node) {
	for i := 0; i < 5; i++ {
		n.Check(context.Background(), 0)
	}
}

func TestFailover_PromotesReplicaWhenPrimaryDies(t *testing.T) {
	cfg := &config.Config{
		Nodes: []config.NodeConfig{
			{URL: "http://meili-0:7700", Role: "primary"},
			{URL: "http://meili-1:7700", Role: "replica"},
			{URL: "http://meili-2:7700", Role: "replica"},
		},
		HealthCheck: config.HealthConfig{
			Interval:           5e9,
			Timeout:            2e9,
			UnhealthyThreshold: 2,
			HealthyThreshold:   2,
		},
	}

	checker := NewChecker(cfg)

	lag := &mockLagProvider{lag: map[string]int64{
		"http://meili-1:7700": 50,
		"http://meili-2:7700": 100, // higher — should be promoted
	}}

	fm := NewFailoverManager(checker, lag)

	// Make primary unhealthy
	nodes := checker.Nodes()
	makeUnhealthy(nodes[0])

	fm.Evaluate()

	// Verify meili-2 was promoted (highest taskUid)
	primary := checker.Primary()
	if primary == nil {
		t.Fatal("expected a primary after failover")
	}
	if primary.URL != "http://meili-2:7700" {
		t.Errorf("expected meili-2 promoted, got %s", primary.URL)
	}

	// Old primary should be demoted to replica
	if nodes[0].Role != "replica" {
		t.Errorf("expected old primary demoted to replica, got %s", nodes[0].Role)
	}

	if !fm.IsFailedOver() {
		t.Error("expected failedOver=true")
	}
}

func TestFailover_PromotesReplicaWithHighestLag(t *testing.T) {
	cfg := &config.Config{
		Nodes: []config.NodeConfig{
			{URL: "http://meili-0:7700", Role: "primary"},
			{URL: "http://meili-1:7700", Role: "replica"},
			{URL: "http://meili-2:7700", Role: "replica"},
		},
		HealthCheck: config.HealthConfig{
			Interval:           5e9,
			Timeout:            2e9,
			UnhealthyThreshold: 2,
			HealthyThreshold:   2,
		},
	}

	checker := NewChecker(cfg)

	lag := &mockLagProvider{lag: map[string]int64{
		"http://meili-1:7700": 200, // higher — should be promoted
		"http://meili-2:7700": 100,
	}}

	fm := NewFailoverManager(checker, lag)
	makeUnhealthy(checker.Nodes()[0])
	fm.Evaluate()

	primary := checker.Primary()
	if primary == nil || primary.URL != "http://meili-1:7700" {
		t.Errorf("expected meili-1 promoted, got %v", primary)
	}
}

func TestFailover_NoHealthyReplica(t *testing.T) {
	cfg := &config.Config{
		Nodes: []config.NodeConfig{
			{URL: "http://meili-0:7700", Role: "primary"},
			{URL: "http://meili-1:7700", Role: "replica"},
		},
		HealthCheck: config.HealthConfig{
			Interval:           5e9,
			Timeout:            2e9,
			UnhealthyThreshold: 2,
			HealthyThreshold:   2,
		},
	}

	checker := NewChecker(cfg)
	fm := NewFailoverManager(checker, nil)

	// Make both nodes unhealthy
	nodes := checker.Nodes()
	makeUnhealthy(nodes[0])
	makeUnhealthy(nodes[1])

	fm.Evaluate()

	// Primary should remain unchanged (still meili-0, just unhealthy)
	if nodes[0].Role != "primary" {
		t.Errorf("expected primary role unchanged when no replica available, got %s", nodes[0].Role)
	}
	if fm.IsFailedOver() {
		t.Error("expected failedOver=false when promotion failed")
	}
}

func TestFailover_RecoverOriginalPrimary(t *testing.T) {
	// Use a real HTTP server for the original primary so we can make it healthy again
	primarySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"available"}`))
	}))
	defer primarySrv.Close()

	cfg := &config.Config{
		Nodes: []config.NodeConfig{
			{URL: primarySrv.URL, Role: "primary"},
			{URL: "http://meili-1:7700", Role: "replica"},
		},
		HealthCheck: config.HealthConfig{
			Interval:           5e9,
			Timeout:            2e9,
			UnhealthyThreshold: 2,
			HealthyThreshold:   2,
		},
	}

	checker := NewChecker(cfg)
	lag := &mockLagProvider{lag: map[string]int64{
		"http://meili-1:7700": 10,
	}}

	fm := NewFailoverManager(checker, lag)
	nodes := checker.Nodes()

	// Step 1: Make primary unhealthy, trigger failover
	makeUnhealthy(nodes[0])
	fm.Evaluate()

	if !fm.IsFailedOver() {
		t.Fatal("expected failover to occur")
	}
	if nodes[1].Role != "primary" {
		t.Errorf("expected meili-1 promoted, got role=%s", nodes[1].Role)
	}

	// Step 2: Original primary recovers (HTTP server responds OK)
	// Need enough successful checks to transition back to healthy
	nodes[0].Check(context.Background(), 2e9)
	nodes[0].Check(context.Background(), 2e9)
	nodes[0].Check(context.Background(), 2e9)

	if !nodes[0].IsHealthy() {
		t.Fatal("expected original primary to be healthy after recovery checks")
	}

	// Step 3: Evaluate — should restore original roles
	fm.Evaluate()

	if fm.IsFailedOver() {
		t.Error("expected failedOver=false after recovery")
	}
	if nodes[0].Role != "primary" {
		t.Errorf("expected original primary restored, got role=%s", nodes[0].Role)
	}
	if nodes[1].Role != "replica" {
		t.Errorf("expected promoted replica demoted back, got role=%s", nodes[1].Role)
	}
}

func TestFailover_NoActionWhenPrimaryHealthy(t *testing.T) {
	cfg := &config.Config{
		Nodes: []config.NodeConfig{
			{URL: "http://meili-0:7700", Role: "primary"},
			{URL: "http://meili-1:7700", Role: "replica"},
		},
		HealthCheck: config.HealthConfig{
			Interval:           5e9,
			Timeout:            2e9,
			UnhealthyThreshold: 3,
			HealthyThreshold:   2,
		},
	}

	checker := NewChecker(cfg)
	fm := NewFailoverManager(checker, nil)

	// Primary is healthy by default — evaluate should be a no-op
	fm.Evaluate()

	if fm.IsFailedOver() {
		t.Error("expected no failover when primary is healthy")
	}
	if checker.Primary().URL != "http://meili-0:7700" {
		t.Error("expected primary unchanged")
	}
}

func TestFailover_NilLagProvider(t *testing.T) {
	cfg := &config.Config{
		Nodes: []config.NodeConfig{
			{URL: "http://meili-0:7700", Role: "primary"},
			{URL: "http://meili-1:7700", Role: "replica"},
		},
		HealthCheck: config.HealthConfig{
			Interval:           5e9,
			Timeout:            2e9,
			UnhealthyThreshold: 2,
			HealthyThreshold:   2,
		},
	}

	checker := NewChecker(cfg)
	fm := NewFailoverManager(checker, nil) // no lag provider

	makeUnhealthy(checker.Nodes()[0])
	fm.Evaluate()

	// Should still promote first healthy replica even without lag info
	primary := checker.Primary()
	if primary == nil || primary.URL != "http://meili-1:7700" {
		t.Errorf("expected meili-1 promoted with nil lag provider, got %v", primary)
	}
}

func TestFailover_IntegratesWithChecker(t *testing.T) {
	// Verify that SetFailoverManager and checkAll integration works
	primarySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer primarySrv.Close()

	replicaSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer replicaSrv.Close()

	cfg := &config.Config{
		Nodes: []config.NodeConfig{
			{URL: primarySrv.URL, Role: "primary"},
			{URL: replicaSrv.URL, Role: "replica"},
		},
		HealthCheck: config.HealthConfig{
			Interval:           5e9,
			Timeout:            2e9,
			UnhealthyThreshold: 3,
			HealthyThreshold:   2,
		},
	}

	checker := NewChecker(cfg)
	fm := NewFailoverManager(checker, nil)
	checker.SetFailoverManager(fm)

	if checker.Failover() != fm {
		t.Error("expected failover manager to be set")
	}

	// checkAll should call Evaluate — just verify no panic
	checker.checkAll(context.Background())
}
