//go:build integration || e2e

package testutil

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"
)

// MeiliClient is a simple test helper for MeiliSearch API calls.
type MeiliClient struct {
	BaseURL string
	APIKey  string
	Client  *http.Client
}

// NewMeiliClient creates a test client for a MeiliSearch endpoint.
func NewMeiliClient(baseURL, apiKey string) *MeiliClient {
	return &MeiliClient{
		BaseURL: baseURL,
		APIKey:  apiKey,
		Client:  &http.Client{Timeout: 10 * time.Second},
	}
}

// HealthCheck returns true if the endpoint responds with 200 OK.
func (c *MeiliClient) HealthCheck() bool {
	req, _ := http.NewRequest(http.MethodGet, c.BaseURL+"/health", nil)
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	resp, err := c.Client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// WaitForHealthy waits until the endpoint is healthy or timeout.
func (c *MeiliClient) WaitForHealthy(t *testing.T, timeout time.Duration) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		if c.HealthCheck() {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("timeout waiting for %s to be healthy", c.BaseURL)
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// AddDocuments adds documents to an index and returns the taskUid.
func (c *MeiliClient) AddDocuments(t *testing.T, index string, docs interface{}) int64 {
	t.Helper()
	body, err := json.Marshal(docs)
	if err != nil {
		t.Fatalf("marshal docs: %v", err)
	}

	req, _ := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/indexes/%s/documents", c.BaseURL, index), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	resp, err := c.Client.Do(req)
	if err != nil {
		t.Fatalf("add documents: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		bodyBytes, _ := io.ReadAll(resp.Body)
		t.Fatalf("add documents: expected 202, got %d: %s", resp.StatusCode, bodyBytes)
	}

	var result struct {
		TaskUID int64 `json:"taskUid"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	return result.TaskUID
}

// Search runs a search query and returns the hits.
func (c *MeiliClient) Search(t *testing.T, index, query string) []map[string]interface{} {
	t.Helper()
	body := fmt.Sprintf(`{"q":%q}`, query)
	req, _ := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/indexes/%s/search", c.BaseURL, index), bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	resp, err := c.Client.Do(req)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		t.Fatalf("search: expected 200, got %d: %s", resp.StatusCode, bodyBytes)
	}

	var result struct {
		Hits []map[string]interface{} `json:"hits"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	return result.Hits
}

// SearchWithRetry runs a search query with retries for eventual consistency.
// Returns hits once the search succeeds, or fails after timeout.
func (c *MeiliClient) SearchWithRetry(t *testing.T, index, query string, minHits int, timeout time.Duration) []map[string]interface{} {
	t.Helper()
	deadline := time.After(timeout)
	for {
		body := fmt.Sprintf(`{"q":%q}`, query)
		req, _ := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/indexes/%s/search", c.BaseURL, index), bytes.NewReader([]byte(body)))
		req.Header.Set("Content-Type", "application/json")
		if c.APIKey != "" {
			req.Header.Set("Authorization", "Bearer "+c.APIKey)
		}

		resp, err := c.Client.Do(req)
		if err == nil && resp.StatusCode == http.StatusOK {
			var result struct {
				Hits []map[string]interface{} `json:"hits"`
			}
			json.NewDecoder(resp.Body).Decode(&result)
			resp.Body.Close()
			if len(result.Hits) >= minHits {
				return result.Hits
			}
		} else if resp != nil {
			resp.Body.Close()
		}

		select {
		case <-deadline:
			t.Fatalf("timeout waiting for search '%s' in index '%s' to return >= %d hits", query, index, minHits)
			return nil
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// WaitForTask waits for a task to complete.
func (c *MeiliClient) WaitForTask(t *testing.T, taskUID int64, timeout time.Duration) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		req, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/tasks/%d", c.BaseURL, taskUID), nil)
		if c.APIKey != "" {
			req.Header.Set("Authorization", "Bearer "+c.APIKey)
		}

		resp, err := c.Client.Do(req)
		if err != nil {
			select {
			case <-deadline:
				t.Fatalf("timeout waiting for task %d", taskUID)
			case <-time.After(200 * time.Millisecond):
				continue
			}
		}

		var result struct {
			Status string `json:"status"`
		}
		json.NewDecoder(resp.Body).Decode(&result)
		resp.Body.Close()

		if result.Status == "succeeded" || result.Status == "failed" {
			if result.Status == "failed" {
				t.Logf("task %d failed", taskUID)
			}
			return
		}

		select {
		case <-deadline:
			t.Fatalf("timeout waiting for task %d (status: %s)", taskUID, result.Status)
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// GetClusterHealth returns the proxy's cluster health response.
func (c *MeiliClient) GetClusterHealth(t *testing.T) (int, map[string]interface{}) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, c.BaseURL+"/health", nil)
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	resp, err := c.Client.Do(req)
	if err != nil {
		t.Fatalf("cluster health: %v", err)
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	return resp.StatusCode, result
}

// SwapIndexes swaps two indexes via raw HTTP (avoids SDK compatibility issues).
func (c *MeiliClient) SwapIndexes(t *testing.T, indexA, indexB string) int64 {
	t.Helper()
	body := fmt.Sprintf(`[{"indexes":[%q,%q]}]`, indexA, indexB)
	req, _ := http.NewRequest(http.MethodPost, c.BaseURL+"/swap-indexes", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	resp, err := c.Client.Do(req)
	if err != nil {
		t.Fatalf("swap indexes: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		bodyBytes, _ := io.ReadAll(resp.Body)
		t.Fatalf("swap indexes: expected 202, got %d: %s", resp.StatusCode, bodyBytes)
	}

	var result struct {
		TaskUID int64 `json:"taskUid"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	return result.TaskUID
}

// DeleteIndex deletes an index (for cleanup).
func (c *MeiliClient) DeleteIndex(t *testing.T, index string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodDelete, fmt.Sprintf("%s/indexes/%s", c.BaseURL, index), nil)
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	resp, err := c.Client.Do(req)
	if err != nil {
		return // best effort
	}
	resp.Body.Close()
}
