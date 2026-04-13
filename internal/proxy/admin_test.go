package proxy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/a-safe-digital/meilisearch-ha-proxy/internal/config"
	"github.com/a-safe-digital/meilisearch-ha-proxy/internal/health"
)

func TestHealthEndpoint_AllHealthy(t *testing.T) {
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

	checker := health.NewChecker(cfg)
	admin := NewAdminHandler(checker, nil)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	admin.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp ClusterHealth
	json.NewDecoder(w.Body).Decode(&resp)

	if resp.Status != "available" {
		t.Errorf("expected status 'available', got %q", resp.Status)
	}
	if resp.HealthyReplicas != 1 {
		t.Errorf("expected 1 healthy replica, got %d", resp.HealthyReplicas)
	}
	if !resp.Primary.Healthy {
		t.Error("expected primary to be healthy")
	}
}

func TestHealthEndpoint_PrimaryHealthyNoReplicas(t *testing.T) {
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
	// Drive replica unhealthy
	nodes := checker.Nodes()
	for i := 0; i < 3; i++ {
		nodes[1].Check(context.Background(), 0)
	}

	admin := NewAdminHandler(checker, nil)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	admin.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp ClusterHealth
	json.NewDecoder(w.Body).Decode(&resp)

	if resp.Status != "degraded" {
		t.Errorf("expected status 'degraded', got %q", resp.Status)
	}
}

func TestHealthEndpoint_PrimaryUnhealthy(t *testing.T) {
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
	// Drive primary unhealthy
	nodes := checker.Nodes()
	for i := 0; i < 3; i++ {
		nodes[0].Check(context.Background(), 0)
	}

	admin := NewAdminHandler(checker, nil)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	admin.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", w.Code)
	}

	var resp ClusterHealth
	json.NewDecoder(w.Body).Decode(&resp)

	if resp.Status != "unavailable" {
		t.Errorf("expected status 'unavailable', got %q", resp.Status)
	}
}

func TestClusterStatusEndpoint(t *testing.T) {
	cfg := &config.Config{
		Nodes: []config.NodeConfig{
			{URL: "http://meili-0:7700", Role: "primary"},
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

	checker := health.NewChecker(cfg)
	admin := NewAdminHandler(checker, nil)

	req := httptest.NewRequest(http.MethodGet, "/cluster/status", nil)
	w := httptest.NewRecorder()
	admin.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp ClusterStatus
	json.NewDecoder(w.Body).Decode(&resp)

	if len(resp.Nodes) != 3 {
		t.Fatalf("expected 3 nodes, got %d", len(resp.Nodes))
	}
	if resp.Nodes[0].Role != "primary" {
		t.Errorf("expected first node role 'primary', got %q", resp.Nodes[0].Role)
	}
	if resp.Nodes[0].State != "healthy" {
		t.Errorf("expected first node state 'healthy', got %q", resp.Nodes[0].State)
	}
	if resp.Nodes[1].Role != "replica" {
		t.Errorf("expected second node role 'replica', got %q", resp.Nodes[1].Role)
	}
}

func TestClusterStatusEndpoint_ContentType(t *testing.T) {
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

	checker := health.NewChecker(cfg)
	admin := NewAdminHandler(checker, nil)

	req := httptest.NewRequest(http.MethodGet, "/cluster/status", nil)
	w := httptest.NewRecorder()
	admin.ServeHTTP(w, req)

	ct := w.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("expected Content-Type 'application/json', got %q", ct)
	}
}

func TestAdminNotFound(t *testing.T) {
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

	checker := health.NewChecker(cfg)
	admin := NewAdminHandler(checker, nil)

	req := httptest.NewRequest(http.MethodGet, "/nonexistent", nil)
	w := httptest.NewRecorder()
	admin.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}
