package health

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/a-safe-digital/meilisearch-ha-proxy/internal/config"
)

func TestChecker_Run_StopsOnCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := &config.Config{
		Nodes: []config.NodeConfig{
			{URL: srv.URL, Role: "primary"},
		},
		HealthCheck: config.HealthConfig{
			Interval:           100 * time.Millisecond,
			Timeout:            2 * time.Second,
			UnhealthyThreshold: 3,
			HealthyThreshold:   2,
		},
	}

	checker := NewChecker(cfg)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		checker.Run(ctx)
		close(done)
	}()

	// Let it run a couple of health check cycles
	time.Sleep(350 * time.Millisecond)

	cancel()

	select {
	case <-done:
		// success — Run exited after cancel
	case <-time.After(5 * time.Second):
		t.Fatal("Checker.Run did not stop after context cancel")
	}
}

func TestChecker_Primary_NoPrimary(t *testing.T) {
	cfg := &config.Config{
		Nodes: []config.NodeConfig{
			{URL: "http://meili-1:7700", Role: "replica"},
			{URL: "http://meili-2:7700", Role: "replica"},
		},
		HealthCheck: config.HealthConfig{
			Interval:           5e9,
			Timeout:            2e9,
			UnhealthyThreshold: 3,
			HealthyThreshold:   2,
		},
	}

	checker := NewChecker(cfg)
	if checker.Primary() != nil {
		t.Error("expected nil primary when no primary configured")
	}
}

func TestChecker_HealthyReplicas_NoneHealthy(t *testing.T) {
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
	// Drive replica unhealthy
	for i := 0; i < 3; i++ {
		checker.nodes[1].recordFailure(errString("test"))
	}

	replicas := checker.HealthyReplicas()
	if len(replicas) != 0 {
		t.Errorf("expected 0 healthy replicas, got %d", len(replicas))
	}
}

func TestChecker_HealthyNodes_NoneHealthy(t *testing.T) {
	cfg := &config.Config{
		Nodes: []config.NodeConfig{
			{URL: "http://meili-0:7700", Role: "primary"},
		},
		HealthCheck: config.HealthConfig{
			Interval:           5e9,
			Timeout:            2e9,
			UnhealthyThreshold: 2,
			HealthyThreshold:   2,
		},
	}

	checker := NewChecker(cfg)
	for i := 0; i < 3; i++ {
		checker.nodes[0].recordFailure(errString("test"))
	}

	nodes := checker.HealthyNodes()
	if len(nodes) != 0 {
		t.Errorf("expected 0 healthy nodes, got %d", len(nodes))
	}
}

func TestChecker_Failover_NilByDefault(t *testing.T) {
	cfg := &config.Config{
		Nodes: []config.NodeConfig{
			{URL: "http://meili-0:7700", Role: "primary"},
		},
		HealthCheck: config.HealthConfig{
			Interval:           5e9,
			Timeout:            2e9,
			UnhealthyThreshold: 3,
			HealthyThreshold:   2,
		},
	}

	checker := NewChecker(cfg)
	if checker.Failover() != nil {
		t.Error("expected nil failover manager by default")
	}
}

func TestChecker_checkAll_WithoutFailoverManager(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := &config.Config{
		Nodes: []config.NodeConfig{
			{URL: srv.URL, Role: "primary"},
		},
		HealthCheck: config.HealthConfig{
			Interval:           5e9,
			Timeout:            2 * time.Second,
			UnhealthyThreshold: 3,
			HealthyThreshold:   2,
		},
	}

	checker := NewChecker(cfg)
	// No failover manager set — should not panic
	checker.checkAll(context.Background())
}

func TestChecker_Run_WithFailoverManager(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := &config.Config{
		Nodes: []config.NodeConfig{
			{URL: srv.URL, Role: "primary"},
			{URL: srv.URL, Role: "replica"},
		},
		HealthCheck: config.HealthConfig{
			Interval:           100 * time.Millisecond,
			Timeout:            2 * time.Second,
			UnhealthyThreshold: 3,
			HealthyThreshold:   2,
		},
	}

	checker := NewChecker(cfg)
	fm := NewFailoverManager(checker, nil)
	checker.SetFailoverManager(fm)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		checker.Run(ctx)
		close(done)
	}()

	time.Sleep(250 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out")
	}
}

func TestNode_LastError_AfterSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := NewNode(srv.URL, "", "primary", 3, 2)
	n.recordFailure(errString("oops"))
	if n.LastError() == nil {
		t.Error("expected error after failure")
	}

	n.recordSuccess()
	if n.LastError() != nil {
		t.Errorf("expected nil error after success, got %v", n.LastError())
	}
}

func TestNode_LastCheck_AfterFailure(t *testing.T) {
	n := NewNode("http://127.0.0.1:19999", "", "primary", 3, 2)
	before := time.Now()
	n.Check(context.Background(), 100*time.Millisecond)
	after := time.Now()

	lc := n.LastCheck()
	if lc.Before(before) || lc.After(after) {
		t.Errorf("LastCheck %v not between %v and %v", lc, before, after)
	}
	if n.LastError() == nil {
		t.Error("expected error after checking unreachable server")
	}
}

func TestNode_UnhealthyToSuspectToHealthy(t *testing.T) {
	// Test the full recovery path with healthyThreshold=3
	n := NewNode("http://test:7700", "", "primary", 2, 3)

	// Drive to Unhealthy
	n.recordFailure(errString("f1"))
	n.recordFailure(errString("f2"))
	if n.State() != Unhealthy {
		t.Fatalf("expected Unhealthy, got %s", n.State())
	}

	// First success: Unhealthy -> Suspect (consecutive success < healthyThreshold)
	n.recordSuccess()
	if n.State() != Suspect {
		t.Fatalf("expected Suspect after 1 success, got %s", n.State())
	}

	// Second success: Suspect -> Healthy
	n.recordSuccess()
	if n.State() != Healthy {
		t.Fatalf("expected Healthy after 2 successes from Suspect, got %s", n.State())
	}
}

func TestNode_UnhealthyDirectToHealthy(t *testing.T) {
	// When healthyThreshold=1, first success should go Unhealthy -> Healthy
	n := NewNode("http://test:7700", "", "primary", 2, 1)

	// Drive to Unhealthy
	n.recordFailure(errString("f1"))
	n.recordFailure(errString("f2"))
	if n.State() != Unhealthy {
		t.Fatalf("expected Unhealthy, got %s", n.State())
	}

	// First success with threshold=1 means consecutive >= threshold
	n.recordSuccess()
	if n.State() != Healthy {
		t.Fatalf("expected Healthy with threshold=1, got %s", n.State())
	}
}

func TestFailover_Evaluate_NoPrimary(t *testing.T) {
	// All replicas — no primary configured
	cfg := &config.Config{
		Nodes: []config.NodeConfig{
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
	fm := NewFailoverManager(checker, nil)

	// Evaluate with no primary — should promote a replica
	fm.Evaluate()

	primary := checker.Primary()
	if primary == nil {
		t.Fatal("expected a replica to be promoted to primary")
	}
}

func TestNode_CheckWithBadURL(t *testing.T) {
	// A node with a URL that makes http.NewRequestWithContext fail
	n := NewNode("http://\x00invalid", "", "primary", 3, 2)
	n.Check(context.Background(), 1*time.Second)

	// Should record failure for bad request creation
	if n.State() != Suspect {
		t.Errorf("expected Suspect after bad URL check, got %s", n.State())
	}
	if n.LastError() == nil {
		t.Error("expected error for bad URL")
	}
}

func TestFailover_RecoverNoOriginalPrimary(t *testing.T) {
	// Both start as replicas, one gets promoted — no "original primary" to recover
	cfg := &config.Config{
		Nodes: []config.NodeConfig{
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
	fm := NewFailoverManager(checker, nil)

	// First evaluate promotes one replica
	fm.Evaluate()
	if !fm.IsFailedOver() {
		t.Fatal("expected failedOver after initial promotion")
	}

	// Second evaluate — current primary is healthy, but no original primary to recover
	// Should remain in failedOver state since tryRecover can't find original primary as replica
	fm.Evaluate()
	if !fm.IsFailedOver() {
		t.Error("expected to remain failedOver when no original primary to recover")
	}
}
