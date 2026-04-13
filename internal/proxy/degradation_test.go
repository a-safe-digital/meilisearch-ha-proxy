package proxy

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/a-safe-digital/meilisearch-ha-proxy/internal/config"
	"github.com/a-safe-digital/meilisearch-ha-proxy/internal/health"
	"github.com/a-safe-digital/meilisearch-ha-proxy/internal/replication"
)

func makeNodeUnhealthy(n *health.Node) {
	for i := 0; i < 5; i++ {
		n.Check(context.Background(), 0)
	}
}

// TestDegradation_AllReplicasDown verifies that reads and writes still work
// when all replicas are down but primary is healthy.
func TestDegradation_AllReplicasDown(t *testing.T) {
	primarySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "search") {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"hits":[{"id":1}]}`))
			return
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"taskUid":1,"status":"enqueued"}`))
	}))
	defer primarySrv.Close()

	cfg := &config.Config{
		Nodes: []config.NodeConfig{
			{URL: primarySrv.URL, Role: "primary"},
			{URL: "http://dead-replica-1:7700", Role: "replica"},
			{URL: "http://dead-replica-2:7700", Role: "replica"},
		},
		HealthCheck: config.HealthConfig{
			Interval:           5e9,
			Timeout:            2e9,
			UnhealthyThreshold: 2,
			HealthyThreshold:   2,
		},
	}

	checker := health.NewChecker(cfg)
	nodes := checker.Nodes()

	// Kill both replicas
	makeNodeUnhealthy(nodes[1])
	makeNodeUnhealthy(nodes[2])

	p := New(checker)

	// Reads should still work (goes to primary, only healthy node)
	req := httptest.NewRequest(http.MethodPost, "/indexes/movies/search", strings.NewReader(`{"q":"test"}`))
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("read: expected 200, got %d", w.Code)
	}

	// Writes should still work (goes to primary)
	req = httptest.NewRequest(http.MethodPost, "/indexes/movies/documents", strings.NewReader(`[{"id":1}]`))
	w = httptest.NewRecorder()
	p.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Errorf("write: expected 202, got %d", w.Code)
	}
}

// TestDegradation_AllNodesDown verifies that the proxy returns 503 for all
// request types when every node is unhealthy.
func TestDegradation_AllNodesDown(t *testing.T) {
	cfg := &config.Config{
		Nodes: []config.NodeConfig{
			{URL: "http://dead-primary:7700", Role: "primary"},
			{URL: "http://dead-replica:7700", Role: "replica"},
		},
		HealthCheck: config.HealthConfig{
			Interval:           5e9,
			Timeout:            2e9,
			UnhealthyThreshold: 2,
			HealthyThreshold:   2,
		},
	}

	checker := health.NewChecker(cfg)
	nodes := checker.Nodes()
	makeNodeUnhealthy(nodes[0])
	makeNodeUnhealthy(nodes[1])

	p := New(checker)

	// Read should return 503
	req := httptest.NewRequest(http.MethodPost, "/indexes/movies/search", strings.NewReader(`{"q":"test"}`))
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("read: expected 503, got %d", w.Code)
	}

	// Write should return 503 (primary unhealthy)
	req = httptest.NewRequest(http.MethodPost, "/indexes/movies/documents", strings.NewReader(`[{"id":1}]`))
	w = httptest.NewRecorder()
	p.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("write: expected 503, got %d", w.Code)
	}
}

// TestDegradation_WritesAfterFailover verifies that after a primary failover,
// writes are routed to the promoted replica.
func TestDegradation_WritesAfterFailover(t *testing.T) {
	var promotedHit bool
	promotedSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		promotedHit = true
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"taskUid":100,"status":"enqueued"}`))
	}))
	defer promotedSrv.Close()

	cfg := &config.Config{
		Nodes: []config.NodeConfig{
			{URL: "http://dead-primary:7700", Role: "primary"},
			{URL: promotedSrv.URL, Role: "replica"},
		},
		HealthCheck: config.HealthConfig{
			Interval:           5e9,
			Timeout:            2e9,
			UnhealthyThreshold: 2,
			HealthyThreshold:   2,
		},
	}

	checker := health.NewChecker(cfg)
	nodes := checker.Nodes()

	// Kill primary
	makeNodeUnhealthy(nodes[0])

	// Set up failover
	fm := health.NewFailoverManager(checker, nil)
	fm.Evaluate()

	// Verify replica was promoted
	primary := checker.Primary()
	if primary == nil || primary.URL != promotedSrv.URL {
		t.Fatalf("expected promoted replica as primary, got %v", primary)
	}

	p := New(checker)

	// Write should go to the promoted replica
	req := httptest.NewRequest(http.MethodPost, "/indexes/movies/documents", strings.NewReader(`[{"id":1}]`))
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)

	if !promotedHit {
		t.Error("expected write to reach the promoted replica")
	}
	if w.Code != http.StatusAccepted {
		t.Errorf("expected 202, got %d", w.Code)
	}
}

// TestDegradation_ReadsAfterFailover verifies that reads still work through
// available healthy nodes after failover.
func TestDegradation_ReadsAfterFailover(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"hits":[]}`))
	}))
	defer srv.Close()

	cfg := &config.Config{
		Nodes: []config.NodeConfig{
			{URL: "http://dead-primary:7700", Role: "primary"},
			{URL: srv.URL, Role: "replica"},
		},
		HealthCheck: config.HealthConfig{
			Interval:           5e9,
			Timeout:            2e9,
			UnhealthyThreshold: 2,
			HealthyThreshold:   2,
		},
	}

	checker := health.NewChecker(cfg)
	nodes := checker.Nodes()

	// Kill primary, trigger failover
	makeNodeUnhealthy(nodes[0])
	fm := health.NewFailoverManager(checker, nil)
	fm.Evaluate()

	p := New(checker)

	// Read should work via the promoted replica
	req := httptest.NewRequest(http.MethodPost, "/indexes/movies/search", strings.NewReader(`{"q":"test"}`))
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

// TestDegradation_ReplicationSkippedWhenNoReplicas verifies that async
// replication is skipped gracefully when no replicas are healthy.
func TestDegradation_ReplicationSkippedWhenNoReplicas(t *testing.T) {
	primarySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"taskUid":5,"status":"enqueued"}`))
	}))
	defer primarySrv.Close()

	cfg := &config.Config{
		Nodes: []config.NodeConfig{
			{URL: primarySrv.URL, Role: "primary"},
			{URL: "http://dead-replica:7700", Role: "replica"},
		},
		HealthCheck: config.HealthConfig{
			Interval:           5e9,
			Timeout:            2e9,
			UnhealthyThreshold: 2,
			HealthyThreshold:   2,
		},
	}

	checker := health.NewChecker(cfg)
	nodes := checker.Nodes()
	makeNodeUnhealthy(nodes[1])

	rep := replication.New(checker, 5*time.Second)
	p := New(checker)
	p.SetReplicator(rep)

	// Write should succeed even though replication can't happen
	req := httptest.NewRequest(http.MethodPost, "/indexes/movies/documents", strings.NewReader(`[{"id":1}]`))
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Errorf("expected 202, got %d", w.Code)
	}

	// Wait for async replication attempt
	<-time.After(100 * time.Millisecond)

	// No failures should be counted (replication was skipped, not failed)
	_, failures := rep.Stats()
	if failures != 0 {
		t.Errorf("expected 0 failures (replication skipped), got %d", failures)
	}
}

// TestDegradation_HealthEndpointReflectsState verifies the /health endpoint
// returns correct status under various degradation scenarios.
func TestDegradation_HealthEndpointReflectsState(t *testing.T) {
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

	checker := health.NewChecker(cfg)
	nodes := checker.Nodes()

	// All healthy → available
	admin := NewAdminHandler(checker, nil)
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	admin.ServeHTTP(w, req)
	body, _ := io.ReadAll(w.Body)

	if w.Code != http.StatusOK {
		t.Errorf("available: expected 200, got %d", w.Code)
	}
	if !strings.Contains(string(body), `"available"`) {
		t.Errorf("expected status 'available', got %s", body)
	}

	// Kill replica → degraded
	makeNodeUnhealthy(nodes[1])
	req = httptest.NewRequest(http.MethodGet, "/health", nil)
	w = httptest.NewRecorder()
	admin.ServeHTTP(w, req)
	body, _ = io.ReadAll(w.Body)

	if w.Code != http.StatusOK {
		t.Errorf("degraded: expected 200, got %d", w.Code)
	}
	if !strings.Contains(string(body), `"degraded"`) {
		t.Errorf("expected status 'degraded', got %s", body)
	}

	// Kill primary → unavailable
	makeNodeUnhealthy(nodes[0])
	req = httptest.NewRequest(http.MethodGet, "/health", nil)
	w = httptest.NewRecorder()
	admin.ServeHTTP(w, req)
	body, _ = io.ReadAll(w.Body)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("unavailable: expected 503, got %d", w.Code)
	}
	if !strings.Contains(string(body), `"unavailable"`) {
		t.Errorf("expected status 'unavailable', got %s", body)
	}
}
