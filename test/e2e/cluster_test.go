//go:build e2e

package e2e

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/meilisearch/meilisearch-go"

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

func strPtr(s string) *string { return &s }

// newSDKClient creates an official meilisearch-go client pointing at the proxy.
func newSDKClient() meilisearch.ServiceManager {
	return meilisearch.New(proxyURL(), meilisearch.WithAPIKey(proxyAPIKey()))
}

// waitForProxy waits until the proxy is healthy using the testutil client.
func waitForProxy(t *testing.T) {
	t.Helper()
	client := testutil.NewMeiliClient(proxyURL(), proxyAPIKey())
	client.WaitForHealthy(t, 60*time.Second)
}

// waitForTask waits for a MeiliSearch task to complete via the SDK.
func waitForTask(t *testing.T, client meilisearch.ServiceManager, taskUID int64) {
	t.Helper()
	task, err := client.WaitForTask(taskUID, 200*time.Millisecond)
	if err != nil {
		t.Fatalf("wait for task %d: %v", taskUID, err)
	}
	if task.Status == meilisearch.TaskStatusFailed {
		t.Fatalf("task %d failed: %s", taskUID, task.Error)
	}
}

// searchWithRetry retries a search until minHits are found or timeout.
func searchWithRetry(t *testing.T, client meilisearch.ServiceManager, index, query string, minHits int, timeout time.Duration) *meilisearch.SearchResponse {
	t.Helper()
	deadline := time.After(timeout)
	for {
		resp, err := client.Index(index).Search(query, &meilisearch.SearchRequest{})
		if err == nil && len(resp.Hits) >= minHits {
			return resp
		}

		select {
		case <-deadline:
			t.Fatalf("timeout waiting for search '%s' in index '%s' to return >= %d hits", query, index, minHits)
			return nil
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// cleanupIndex deletes an index (best effort).
func cleanupIndex(client meilisearch.ServiceManager, uid string) {
	client.DeleteIndex(uid)
}

// docOpts returns DocumentOptions with primary key "id".
func docOpts() *meilisearch.DocumentOptions {
	return &meilisearch.DocumentOptions{PrimaryKey: strPtr("id")}
}

// --- Tests using official meilisearch-go SDK ---

func TestE2E_ProxyHealthy(t *testing.T) {
	waitForProxy(t)
	client := newSDKClient()

	health, err := client.Health()
	if err != nil {
		t.Fatalf("health check failed: %v", err)
	}
	if health.Status != "available" {
		t.Errorf("expected 'available', got %q", health.Status)
	}
}

func TestE2E_SDK_CreateIndexAndSearch(t *testing.T) {
	waitForProxy(t)
	client := newSDKClient()
	indexUID := "e2e-sdk-movies"
	defer cleanupIndex(client, indexUID)

	// Create index through proxy
	taskInfo, err := client.CreateIndex(&meilisearch.IndexConfig{
		Uid:        indexUID,
		PrimaryKey: "id",
	})
	if err != nil {
		t.Fatalf("create index: %v", err)
	}
	waitForTask(t, client, taskInfo.TaskUID)

	// Add documents using the SDK with typed structs
	type Movie struct {
		ID    int    `json:"id"`
		Title string `json:"title"`
		Year  int    `json:"year"`
		Genre string `json:"genre"`
	}

	docs := []Movie{
		{ID: 1, Title: "The Matrix", Year: 1999, Genre: "sci-fi"},
		{ID: 2, Title: "Inception", Year: 2010, Genre: "sci-fi"},
		{ID: 3, Title: "The Godfather", Year: 1972, Genre: "crime"},
		{ID: 4, Title: "Pulp Fiction", Year: 1994, Genre: "crime"},
		{ID: 5, Title: "Interstellar", Year: 2014, Genre: "sci-fi"},
	}

	taskInfo, err = client.Index(indexUID).AddDocuments(docs, docOpts())
	if err != nil {
		t.Fatalf("add documents: %v", err)
	}
	waitForTask(t, client, taskInfo.TaskUID)
	t.Logf("added %d documents, taskUID=%d", len(docs), taskInfo.TaskUID)

	// Search with retry (eventual consistency across replicas)
	resp := searchWithRetry(t, client, indexUID, "Matrix", 1, 30*time.Second)
	if len(resp.Hits) != 1 {
		t.Errorf("expected 1 hit for 'Matrix', got %d", len(resp.Hits))
	}

	// Search for genre
	resp = searchWithRetry(t, client, indexUID, "sci-fi", 3, 15*time.Second)
	if len(resp.Hits) < 3 {
		t.Errorf("expected at least 3 hits for 'sci-fi', got %d", len(resp.Hits))
	}
}

func TestE2E_SDK_UpdateDocuments(t *testing.T) {
	waitForProxy(t)
	client := newSDKClient()
	indexUID := "e2e-sdk-updates"
	defer cleanupIndex(client, indexUID)

	// Add initial documents
	taskInfo, err := client.Index(indexUID).AddDocuments([]map[string]interface{}{
		{"id": 1, "title": "Original Title", "status": "draft"},
		{"id": 2, "title": "Another Doc", "status": "draft"},
	}, docOpts())
	if err != nil {
		t.Fatalf("add documents: %v", err)
	}
	waitForTask(t, client, taskInfo.TaskUID)

	// Update documents through proxy
	taskInfo, err = client.Index(indexUID).UpdateDocuments([]map[string]interface{}{
		{"id": 1, "title": "Updated Title", "status": "published"},
	}, docOpts())
	if err != nil {
		t.Fatalf("update documents: %v", err)
	}
	waitForTask(t, client, taskInfo.TaskUID)

	// Verify update via search
	resp := searchWithRetry(t, client, indexUID, "Updated Title", 1, 15*time.Second)
	if len(resp.Hits) != 1 {
		t.Errorf("expected 1 hit for 'Updated Title', got %d", len(resp.Hits))
	}
}

func TestE2E_SDK_DeleteDocuments(t *testing.T) {
	waitForProxy(t)
	client := newSDKClient()
	indexUID := "e2e-sdk-deletes"
	defer cleanupIndex(client, indexUID)

	// Add documents
	taskInfo, err := client.Index(indexUID).AddDocuments([]map[string]interface{}{
		{"id": 1, "title": "Keep This"},
		{"id": 2, "title": "Delete This"},
		{"id": 3, "title": "Also Keep"},
	}, docOpts())
	if err != nil {
		t.Fatalf("add documents: %v", err)
	}
	waitForTask(t, client, taskInfo.TaskUID)

	// Verify all 3 exist
	searchWithRetry(t, client, indexUID, "", 3, 15*time.Second)

	// Delete one document through proxy
	taskInfo, err = client.Index(indexUID).DeleteDocument("2", nil)
	if err != nil {
		t.Fatalf("delete document: %v", err)
	}
	waitForTask(t, client, taskInfo.TaskUID)

	// Verify only 2 remain — retry since replicas need time
	deadline := time.After(15 * time.Second)
	for {
		resp, err := client.Index(indexUID).Search("", &meilisearch.SearchRequest{})
		if err == nil && len(resp.Hits) == 2 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("timeout: expected 2 documents after delete")
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func TestE2E_SDK_Settings(t *testing.T) {
	waitForProxy(t)
	client := newSDKClient()
	indexUID := "e2e-sdk-settings"
	defer cleanupIndex(client, indexUID)

	// Create index
	taskInfo, err := client.CreateIndex(&meilisearch.IndexConfig{
		Uid:        indexUID,
		PrimaryKey: "id",
	})
	if err != nil {
		t.Fatalf("create index: %v", err)
	}
	waitForTask(t, client, taskInfo.TaskUID)

	// Update filterable attributes through proxy
	filterableAttrs := []interface{}{"genre", "year"}
	taskInfo, err = client.Index(indexUID).UpdateFilterableAttributes(&filterableAttrs)
	if err != nil {
		t.Fatalf("update filterable attributes: %v", err)
	}
	waitForTask(t, client, taskInfo.TaskUID)

	// Add documents with filterable fields
	taskInfo, err = client.Index(indexUID).AddDocuments([]map[string]interface{}{
		{"id": 1, "title": "Action Movie", "genre": "action", "year": 2024},
		{"id": 2, "title": "Drama Film", "genre": "drama", "year": 2024},
		{"id": 3, "title": "Old Action", "genre": "action", "year": 2000},
	}, docOpts())
	if err != nil {
		t.Fatalf("add documents: %v", err)
	}
	waitForTask(t, client, taskInfo.TaskUID)

	// Search with filter (eventual consistency retry)
	deadline := time.After(30 * time.Second)
	for {
		resp, err := client.Index(indexUID).Search("", &meilisearch.SearchRequest{
			Filter: "genre = action",
		})
		if err == nil && len(resp.Hits) == 2 {
			t.Logf("filtered search returned %d hits", len(resp.Hits))
			break
		}
		select {
		case <-deadline:
			t.Fatalf("timeout: expected 2 action movies with filter")
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func TestE2E_SDK_MultiSearch(t *testing.T) {
	waitForProxy(t)
	client := newSDKClient()
	indexUID1 := "e2e-sdk-multi-1"
	indexUID2 := "e2e-sdk-multi-2"
	defer cleanupIndex(client, indexUID1)
	defer cleanupIndex(client, indexUID2)

	// Create two indexes with documents
	taskInfo, err := client.Index(indexUID1).AddDocuments([]map[string]interface{}{
		{"id": 1, "title": "Alpha One"},
		{"id": 2, "title": "Alpha Two"},
	}, docOpts())
	if err != nil {
		t.Fatalf("add docs to index 1: %v", err)
	}
	waitForTask(t, client, taskInfo.TaskUID)

	taskInfo, err = client.Index(indexUID2).AddDocuments([]map[string]interface{}{
		{"id": 1, "title": "Beta One"},
		{"id": 2, "title": "Beta Two"},
	}, docOpts())
	if err != nil {
		t.Fatalf("add docs to index 2: %v", err)
	}
	waitForTask(t, client, taskInfo.TaskUID)

	// Multi-search through proxy with retry
	deadline := time.After(30 * time.Second)
	for {
		resp, err := client.MultiSearch(&meilisearch.MultiSearchRequest{
			Queries: []*meilisearch.SearchRequest{
				{IndexUID: indexUID1, Query: "Alpha"},
				{IndexUID: indexUID2, Query: "Beta"},
			},
		})
		if err == nil && len(resp.Results) == 2 &&
			len(resp.Results[0].Hits) >= 2 && len(resp.Results[1].Hits) >= 2 {
			t.Logf("multi-search returned %d results: [%d, %d] hits",
				len(resp.Results), len(resp.Results[0].Hits), len(resp.Results[1].Hits))
			break
		}
		select {
		case <-deadline:
			t.Fatalf("timeout: multi-search didn't return expected results")
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func TestE2E_SDK_LargeBatch(t *testing.T) {
	waitForProxy(t)
	client := newSDKClient()
	indexUID := "e2e-sdk-large"
	defer cleanupIndex(client, indexUID)

	// Create 500 documents in one batch
	docs := make([]map[string]interface{}, 500)
	for i := range docs {
		docs[i] = map[string]interface{}{
			"id":       i + 1,
			"title":    fmt.Sprintf("Document %d", i+1),
			"category": fmt.Sprintf("cat-%d", i%10),
		}
	}

	taskInfo, err := client.Index(indexUID).AddDocuments(docs, docOpts())
	if err != nil {
		t.Fatalf("add 500 documents: %v", err)
	}
	waitForTask(t, client, taskInfo.TaskUID)

	// Verify via search — default limit is 20
	resp := searchWithRetry(t, client, indexUID, "Document", 20, 30*time.Second)
	t.Logf("large batch search returned %d hits (default limit)", len(resp.Hits))

	// Search with explicit limit
	deadline := time.After(30 * time.Second)
	for {
		resp, err := client.Index(indexUID).Search("Document", &meilisearch.SearchRequest{
			Limit: 100,
		})
		if err == nil && len(resp.Hits) == 100 {
			t.Logf("large batch with limit=100 returned %d hits", len(resp.Hits))
			break
		}
		select {
		case <-deadline:
			t.Fatalf("timeout: expected 100 hits with limit=100")
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func TestE2E_SDK_TasksEndpoint(t *testing.T) {
	waitForProxy(t)
	client := newSDKClient()
	indexUID := "e2e-sdk-tasks"
	defer cleanupIndex(client, indexUID)

	// Create some tasks
	taskInfo, err := client.Index(indexUID).AddDocuments([]map[string]interface{}{
		{"id": 1, "title": "Task Test"},
	}, docOpts())
	if err != nil {
		t.Fatalf("add document: %v", err)
	}
	waitForTask(t, client, taskInfo.TaskUID)

	// Query tasks through proxy (this exercises the admin fallback path)
	task, err := client.GetTask(taskInfo.TaskUID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if task.Status != meilisearch.TaskStatusSucceeded {
		t.Errorf("expected task status 'succeeded', got %q", task.Status)
	}
	t.Logf("task %d: status=%s, type=%s", task.UID, task.Status, task.Type)

	// List tasks through proxy
	tasks, err := client.GetTasks(&meilisearch.TasksQuery{
		IndexUIDS: []string{indexUID},
		Limit:     10,
	})
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(tasks.Results) == 0 {
		t.Error("expected at least 1 task in task list")
	}
	t.Logf("found %d tasks for index %s", len(tasks.Results), indexUID)
}

func TestE2E_SDK_Version(t *testing.T) {
	waitForProxy(t)
	client := newSDKClient()

	// Get version through proxy (admin fallback to primary)
	version, err := client.Version()
	if err != nil {
		t.Fatalf("get version: %v", err)
	}
	if version.PkgVersion == "" {
		t.Error("expected non-empty package version")
	}
	t.Logf("MeiliSearch version: %s", version.PkgVersion)
}

func TestE2E_SDK_Stats(t *testing.T) {
	waitForProxy(t)
	client := newSDKClient()
	indexUID := "e2e-sdk-stats"
	defer cleanupIndex(client, indexUID)

	// Add documents
	taskInfo, err := client.Index(indexUID).AddDocuments([]map[string]interface{}{
		{"id": 1, "title": "Stats Test"},
	}, docOpts())
	if err != nil {
		t.Fatalf("add document: %v", err)
	}
	waitForTask(t, client, taskInfo.TaskUID)

	// Get stats through proxy
	stats, err := client.GetStats()
	if err != nil {
		t.Fatalf("get stats: %v", err)
	}
	t.Logf("database size: %d bytes, indexes: %v", stats.DatabaseSize, stats.Indexes)
}

func TestE2E_SDK_Keys(t *testing.T) {
	waitForProxy(t)
	client := newSDKClient()

	// List API keys through proxy (admin endpoint forwarded to primary)
	keys, err := client.GetKeys(&meilisearch.KeysQuery{Limit: 10})
	if err != nil {
		t.Fatalf("get keys: %v", err)
	}
	t.Logf("found %d API keys", len(keys.Results))
}

func TestE2E_SDK_SwapIndexes(t *testing.T) {
	waitForProxy(t)
	client := newSDKClient()
	httpClient := testutil.NewMeiliClient(proxyURL(), proxyAPIKey())
	indexA := "e2e-sdk-swap-a"
	indexB := "e2e-sdk-swap-b"
	defer cleanupIndex(client, indexA)
	defer cleanupIndex(client, indexB)

	// Create two indexes with different documents
	taskInfo, err := client.Index(indexA).AddDocuments([]map[string]interface{}{
		{"id": 1, "title": "Index A Content"},
	}, docOpts())
	if err != nil {
		t.Fatalf("add to A: %v", err)
	}
	waitForTask(t, client, taskInfo.TaskUID)

	taskInfo, err = client.Index(indexB).AddDocuments([]map[string]interface{}{
		{"id": 1, "title": "Index B Content"},
	}, docOpts())
	if err != nil {
		t.Fatalf("add to B: %v", err)
	}
	waitForTask(t, client, taskInfo.TaskUID)

	// Wait for replication
	searchWithRetry(t, client, indexA, "Index A", 1, 15*time.Second)
	searchWithRetry(t, client, indexB, "Index B", 1, 15*time.Second)

	// Swap indexes through proxy (use raw HTTP — SDK v0.36.2 sends
	// a "rename" field that MeiliSearch <1.13 rejects as unknown)
	swapTaskUID := httpClient.SwapIndexes(t, indexA, indexB)
	waitForTask(t, client, swapTaskUID)

	// Verify swap — Index A should now have B's content
	deadline := time.After(30 * time.Second)
	for {
		resp, err := client.Index(indexA).Search("Index B", &meilisearch.SearchRequest{})
		if err == nil && len(resp.Hits) == 1 {
			t.Log("swap verified: index A now has B's content")
			break
		}
		select {
		case <-deadline:
			t.Fatalf("timeout: swap didn't replicate")
		case <-time.After(500 * time.Millisecond):
		}
	}
}
