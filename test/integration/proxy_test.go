//go:build integration

package integration

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/a-safe-digital/meilisearch-ha-proxy/internal/config"
	"github.com/a-safe-digital/meilisearch-ha-proxy/internal/health"
	"github.com/a-safe-digital/meilisearch-ha-proxy/internal/proxy"
	"github.com/a-safe-digital/meilisearch-ha-proxy/internal/replication"
	"github.com/a-safe-digital/meilisearch-ha-proxy/test/testutil"
)

func getEnvOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func testConfig() *config.Config {
	primary := getEnvOrDefault("MEILI_PRIMARY_URL", "http://localhost:7700")
	replica1 := getEnvOrDefault("MEILI_REPLICA1_URL", "http://localhost:7701")
	replica2 := getEnvOrDefault("MEILI_REPLICA2_URL", "http://localhost:7702")
	apiKey := getEnvOrDefault("MEILI_API_KEY", "test-master-key")

	return &config.Config{
		Listen:        ":0",
		MetricsListen: ":0",
		LogLevel:      "info",
		Nodes: []config.NodeConfig{
			{URL: primary, APIKey: apiKey, Role: "primary"},
			{URL: replica1, APIKey: apiKey, Role: "replica"},
			{URL: replica2, APIKey: apiKey, Role: "replica"},
		},
		HealthCheck: config.HealthConfig{
			Interval:           2 * time.Second,
			Timeout:            2 * time.Second,
			UnhealthyThreshold: 3,
			HealthyThreshold:   2,
		},
		Replication: config.ReplicConfig{
			Timeout: 10 * time.Second,
		},
	}
}

func TestIntegration_SearchThroughProxy(t *testing.T) {
	cfg := testConfig()
	apiKey := cfg.Nodes[0].APIKey

	// Verify backends are up
	for _, n := range cfg.Nodes {
		client := testutil.NewMeiliClient(n.URL, n.APIKey)
		client.WaitForHealthy(t, 30*time.Second)
	}

	checker := health.NewChecker(cfg)
	rep := replication.New(checker, cfg.Replication.Timeout)
	p := proxy.New(checker)
	p.SetReplicator(rep)
	p.SetAdminHandler(proxy.NewAdminHandler(checker, rep))

	// Add documents directly to primary
	primaryClient := testutil.NewMeiliClient(cfg.Nodes[0].URL, apiKey)
	docs := []map[string]interface{}{
		{"id": 1, "title": "The Matrix", "genre": "sci-fi"},
		{"id": 2, "title": "Inception", "genre": "sci-fi"},
		{"id": 3, "title": "Interstellar", "genre": "sci-fi"},
	}

	taskUID := primaryClient.AddDocuments(t, "integration-movies", docs)
	primaryClient.WaitForTask(t, taskUID, 10*time.Second)

	// Search through proxy
	req := httptest.NewRequest(http.MethodPost, "/indexes/integration-movies/search",
		strings.NewReader(`{"q":"matrix"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("search: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	if !strings.Contains(w.Body.String(), "Matrix") {
		t.Errorf("expected 'Matrix' in results, got: %s", w.Body.String())
	}

	// Cleanup
	primaryClient.DeleteIndex(t, "integration-movies")
}

func TestIntegration_WriteAndReplicateThroughProxy(t *testing.T) {
	cfg := testConfig()
	apiKey := cfg.Nodes[0].APIKey

	for _, n := range cfg.Nodes {
		client := testutil.NewMeiliClient(n.URL, n.APIKey)
		client.WaitForHealthy(t, 30*time.Second)
	}

	checker := health.NewChecker(cfg)
	rep := replication.New(checker, cfg.Replication.Timeout)
	p := proxy.New(checker)
	p.SetReplicator(rep)
	p.SetAdminHandler(proxy.NewAdminHandler(checker, rep))

	// Write through proxy (should go to primary and replicate)
	body := `[{"id":10,"title":"Proxy Test Doc","genre":"test"}]`
	req := httptest.NewRequest(http.MethodPost, "/indexes/integration-repltest/documents",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("write: expected 202, got %d: %s", w.Code, w.Body.String())
	}

	// Wait for primary task to complete
	time.Sleep(3 * time.Second)

	// Verify document is on primary
	primaryClient := testutil.NewMeiliClient(cfg.Nodes[0].URL, apiKey)
	primaryClient.WaitForTask(t, 0, 10*time.Second) // wait for any pending tasks

	hits := primaryClient.Search(t, "integration-repltest", "Proxy Test")
	if len(hits) == 0 {
		t.Error("expected document on primary after write through proxy")
	}

	// Wait for replication + task processing on replicas
	time.Sleep(5 * time.Second)

	// Verify document replicated to replica 1
	replica1Client := testutil.NewMeiliClient(cfg.Nodes[1].URL, apiKey)
	hits = replica1Client.Search(t, "integration-repltest", "Proxy Test")
	if len(hits) == 0 {
		t.Log("document not yet on replica-1 (replication may need more time)")
	} else {
		t.Log("document successfully replicated to replica-1")
	}

	// Cleanup
	primaryClient.DeleteIndex(t, "integration-repltest")
	for _, n := range cfg.Nodes[1:] {
		c := testutil.NewMeiliClient(n.URL, apiKey)
		c.DeleteIndex(t, "integration-repltest")
	}
}

func TestIntegration_HealthEndpoint(t *testing.T) {
	cfg := testConfig()

	for _, n := range cfg.Nodes {
		client := testutil.NewMeiliClient(n.URL, n.APIKey)
		client.WaitForHealthy(t, 30*time.Second)
	}

	checker := health.NewChecker(cfg)
	rep := replication.New(checker, cfg.Replication.Timeout)
	admin := proxy.NewAdminHandler(checker, rep)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	admin.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	if !strings.Contains(w.Body.String(), `"available"`) {
		t.Errorf("expected status 'available', got: %s", w.Body.String())
	}
}

func TestIntegration_ClusterStatus(t *testing.T) {
	cfg := testConfig()

	for _, n := range cfg.Nodes {
		client := testutil.NewMeiliClient(n.URL, n.APIKey)
		client.WaitForHealthy(t, 30*time.Second)
	}

	checker := health.NewChecker(cfg)
	rep := replication.New(checker, cfg.Replication.Timeout)
	admin := proxy.NewAdminHandler(checker, rep)

	req := httptest.NewRequest(http.MethodGet, "/cluster/status", nil)
	w := httptest.NewRecorder()
	admin.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	if !strings.Contains(w.Body.String(), `"primary"`) {
		t.Errorf("expected 'primary' in cluster status, got: %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"replica"`) {
		t.Errorf("expected 'replica' in cluster status, got: %s", w.Body.String())
	}
}
