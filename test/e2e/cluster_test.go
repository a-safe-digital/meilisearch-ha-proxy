//go:build e2e

package e2e

import (
	"os"
	"testing"
	"time"

	"github.com/a-safe-digital/meilisearch-ha-proxy/test/testutil"
)

func getEnvOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func proxyURL() string {
	return getEnvOrDefault("PROXY_URL", "http://localhost:7700")
}

func proxyAPIKey() string {
	return getEnvOrDefault("PROXY_API_KEY", "test-master-key")
}

func TestE2E_ProxyHealthy(t *testing.T) {
	client := testutil.NewMeiliClient(proxyURL(), proxyAPIKey())
	client.WaitForHealthy(t, 60*time.Second)

	code, health := client.GetClusterHealth(t)
	if code != 200 {
		t.Errorf("expected 200, got %d", code)
	}

	status, _ := health["status"].(string)
	if status != "available" {
		t.Errorf("expected 'available', got %q", status)
	}
}

func TestE2E_WriteAndSearchThroughProxy(t *testing.T) {
	client := testutil.NewMeiliClient(proxyURL(), proxyAPIKey())
	client.WaitForHealthy(t, 60*time.Second)

	// Add documents through proxy
	docs := []map[string]interface{}{
		{"id": 1, "title": "E2E Test Movie One", "year": 2024},
		{"id": 2, "title": "E2E Test Movie Two", "year": 2025},
		{"id": 3, "title": "E2E Unique Document", "year": 2026},
	}

	taskUID := client.AddDocuments(t, "e2e-movies", docs)
	t.Logf("write accepted, taskUid=%d", taskUID)

	// Wait for task to complete on primary
	client.WaitForTask(t, taskUID, 30*time.Second)

	// Search with retry for eventual consistency across replicas
	hits := client.SearchWithRetry(t, "e2e-movies", "E2E Test", 2, 30*time.Second)
	if len(hits) < 2 {
		t.Errorf("expected at least 2 hits for 'E2E Test', got %d", len(hits))
	}

	// Search for specific document
	hits = client.SearchWithRetry(t, "e2e-movies", "Unique Document", 1, 15*time.Second)
	if len(hits) != 1 {
		t.Errorf("expected 1 hit for 'Unique Document', got %d", len(hits))
	}

	// Cleanup
	client.DeleteIndex(t, "e2e-movies")
}

func TestE2E_MultipleWritesThenSearch(t *testing.T) {
	client := testutil.NewMeiliClient(proxyURL(), proxyAPIKey())
	client.WaitForHealthy(t, 60*time.Second)

	// Write multiple batches
	var lastTaskUID int64
	for batch := 0; batch < 3; batch++ {
		docs := []map[string]interface{}{
			{"id": batch*10 + 1, "title": "Batch Doc", "batch": batch},
			{"id": batch*10 + 2, "title": "Batch Doc", "batch": batch},
		}
		lastTaskUID = client.AddDocuments(t, "e2e-batches", docs)
	}
	client.WaitForTask(t, lastTaskUID, 30*time.Second)

	// Should have all 6 documents
	hits := client.SearchWithRetry(t, "e2e-batches", "Batch Doc", 6, 30*time.Second)
	if len(hits) < 6 {
		t.Errorf("expected 6 hits across 3 batches, got %d", len(hits))
	}

	client.DeleteIndex(t, "e2e-batches")
}

func TestE2E_DeleteThroughProxy(t *testing.T) {
	client := testutil.NewMeiliClient(proxyURL(), proxyAPIKey())
	client.WaitForHealthy(t, 60*time.Second)

	// Add documents
	docs := []map[string]interface{}{
		{"id": 1, "title": "Delete Me"},
		{"id": 2, "title": "Keep Me"},
	}
	taskUID := client.AddDocuments(t, "e2e-delete", docs)
	client.WaitForTask(t, taskUID, 15*time.Second)

	// Verify docs exist
	client.SearchWithRetry(t, "e2e-delete", "", 2, 15*time.Second)

	// Delete the index entirely
	client.DeleteIndex(t, "e2e-delete")
	time.Sleep(2 * time.Second)

	t.Log("index deleted successfully")
}

func TestE2E_LargeBatch(t *testing.T) {
	client := testutil.NewMeiliClient(proxyURL(), proxyAPIKey())
	client.WaitForHealthy(t, 60*time.Second)

	// Write 100 documents in one batch
	docs := make([]map[string]interface{}, 100)
	for i := range docs {
		docs[i] = map[string]interface{}{
			"id":    i + 1,
			"title": "Large Batch Document",
			"index": i,
		}
	}

	taskUID := client.AddDocuments(t, "e2e-large", docs)
	client.WaitForTask(t, taskUID, 30*time.Second)

	hits := client.SearchWithRetry(t, "e2e-large", "Large Batch", 20, 30*time.Second)
	if len(hits) < 20 { // MeiliSearch returns max 20 by default
		t.Errorf("expected 20 hits (default limit), got %d", len(hits))
	}

	client.DeleteIndex(t, "e2e-large")
}
