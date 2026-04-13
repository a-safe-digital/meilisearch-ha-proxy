package health

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/a-safe-digital/meilisearch-ha-proxy/internal/config"
)

func TestNewCheckerCreatesNodes(t *testing.T) {
	cfg := &config.Config{
		Nodes: []config.NodeConfig{
			{URL: "http://meili-0:7700", APIKey: "key0", Role: "primary"},
			{URL: "http://meili-1:7700", APIKey: "key1", Role: "replica"},
			{URL: "http://meili-2:7700", APIKey: "key2", Role: "replica"},
		},
		HealthCheck: config.HealthConfig{
			Interval:           5e9,
			Timeout:            2e9,
			UnhealthyThreshold: 3,
			HealthyThreshold:   2,
		},
	}

	c := NewChecker(cfg)
	if len(c.Nodes()) != 3 {
		t.Errorf("expected 3 nodes, got %d", len(c.Nodes()))
	}
}

func TestPrimary(t *testing.T) {
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

	c := NewChecker(cfg)
	p := c.Primary()
	if p == nil {
		t.Fatal("expected primary, got nil")
	}
	if p.URL != "http://meili-0:7700" {
		t.Errorf("expected meili-0, got %s", p.URL)
	}
}

func TestHealthyReplicas(t *testing.T) {
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

	c := NewChecker(cfg)

	// All nodes start healthy
	replicas := c.HealthyReplicas()
	if len(replicas) != 2 {
		t.Errorf("expected 2 healthy replicas initially, got %d", len(replicas))
	}

	// Drive one replica to unhealthy
	for i := 0; i < 3; i++ {
		c.nodes[2].recordFailure(errString("test"))
	}
	replicas = c.HealthyReplicas()
	if len(replicas) != 1 {
		t.Errorf("expected 1 healthy replica after driving one unhealthy, got %d", len(replicas))
	}
}

func TestHealthyNodes(t *testing.T) {
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
			Interval:           5e9,
			Timeout:            2e9,
			UnhealthyThreshold: 3,
			HealthyThreshold:   2,
		},
	}

	c := NewChecker(cfg)
	healthy := c.HealthyNodes()
	if len(healthy) != 2 {
		t.Errorf("expected 2 healthy nodes, got %d", len(healthy))
	}
}
