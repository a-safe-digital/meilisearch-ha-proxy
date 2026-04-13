package replication

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/a-safe-digital/meilisearch-ha-proxy/internal/config"
	"github.com/a-safe-digital/meilisearch-ha-proxy/internal/health"
)

func TestCaptureWriteFromReader(t *testing.T) {
	body := `[{"id":1,"title":"Test"}]`
	req := httptest.NewRequest(http.MethodPost, "/indexes/movies/documents", strings.NewReader(body))

	captured, err := CaptureWriteFromReader(req, req.Body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(captured) != body {
		t.Errorf("expected %q, got %q", body, string(captured))
	}

	// Verify body was replaced and is re-readable
	buf := make([]byte, len(body))
	n, _ := req.Body.Read(buf)
	if string(buf[:n]) != body {
		t.Errorf("body not re-readable, got %q", string(buf[:n]))
	}
}

func TestCaptureWriteFromReader_NilBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodDelete, "/indexes/movies", nil)
	captured, err := CaptureWriteFromReader(req, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(captured) != 0 {
		t.Errorf("expected empty, got %v", captured)
	}
}

func TestReplicateAsync_NoHealthyReplicas(t *testing.T) {
	// All replicas are unhealthy — should warn but not panic
	cfg := &config.Config{
		Nodes: []config.NodeConfig{
			{URL: "http://primary:7700", Role: "primary"},
			{URL: "http://dead-replica:7700", Role: "replica"},
		},
		HealthCheck: config.HealthConfig{
			Interval: 5e9, Timeout: 2e9, UnhealthyThreshold: 2, HealthyThreshold: 2,
		},
	}

	checker := health.NewChecker(cfg)
	// Drive replicas unhealthy
	nodes := checker.Nodes()
	for _, n := range nodes {
		if n.GetRole() == "replica" {
			for i := 0; i < 3; i++ {
				n.Check(t.Context(), 0)
			}
		}
	}

	rep := New(checker, 1*time.Second)
	// Should not panic
	rep.ReplicateAsync(WriteRecord{
		Method:  http.MethodPost,
		Path:    "/indexes/test/documents",
		Body:    []byte(`[{"id":1}]`),
		TaskUID: 99,
	})
}

func TestReplicateAsync_TaskUID0(t *testing.T) {
	var mu sync.Mutex
	var gotTaskID string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotTaskID = r.Header.Get("TaskId")
		mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	cfg := &config.Config{
		Nodes: []config.NodeConfig{
			{URL: "http://primary:7700", Role: "primary"},
			{URL: srv.URL, Role: "replica"},
		},
		HealthCheck: config.HealthConfig{
			Interval: 5e9, Timeout: 2e9, UnhealthyThreshold: 3, HealthyThreshold: 2,
		},
	}

	checker := health.NewChecker(cfg)
	rep := New(checker, 5*time.Second)

	rep.ReplicateAsync(WriteRecord{
		Method:  http.MethodPost,
		Path:    "/indexes/test/documents",
		Body:    []byte(`[{"id":1}]`),
		TaskUID: 0, // zero — should not set TaskId header
	})

	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	tid := gotTaskID
	mu.Unlock()
	if tid != "" {
		t.Errorf("expected no TaskId header for taskUID=0, got %q", tid)
	}
}

func TestStats_InitiallyZero(t *testing.T) {
	cfg := &config.Config{
		Nodes: []config.NodeConfig{
			{URL: "http://primary:7700", Role: "primary"},
		},
		HealthCheck: config.HealthConfig{
			Interval: 5e9, Timeout: 2e9, UnhealthyThreshold: 3, HealthyThreshold: 2,
		},
	}
	checker := health.NewChecker(cfg)
	rep := New(checker, 5*time.Second)

	success, failure := rep.Stats()
	if success != 0 || failure != 0 {
		t.Errorf("expected 0/0, got %d/%d", success, failure)
	}
}

func TestReplicaLag_Empty(t *testing.T) {
	cfg := &config.Config{
		Nodes: []config.NodeConfig{
			{URL: "http://primary:7700", Role: "primary"},
		},
		HealthCheck: config.HealthConfig{
			Interval: 5e9, Timeout: 2e9, UnhealthyThreshold: 3, HealthyThreshold: 2,
		},
	}
	checker := health.NewChecker(cfg)
	rep := New(checker, 5*time.Second)

	lag := rep.ReplicaLag()
	if len(lag) != 0 {
		t.Errorf("expected empty lag map, got %v", lag)
	}
}

func TestReplicateAsync_CopiesHeaders(t *testing.T) {
	var mu sync.Mutex
	var gotContentType string
	var gotCustom string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotContentType = r.Header.Get("Content-Type")
		gotCustom = r.Header.Get("X-Custom")
		mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	cfg := &config.Config{
		Nodes: []config.NodeConfig{
			{URL: "http://primary:7700", Role: "primary"},
			{URL: srv.URL, Role: "replica"},
		},
		HealthCheck: config.HealthConfig{
			Interval: 5e9, Timeout: 2e9, UnhealthyThreshold: 3, HealthyThreshold: 2,
		},
	}
	checker := health.NewChecker(cfg)
	rep := New(checker, 5*time.Second)

	headers := http.Header{}
	headers.Set("Content-Type", "application/json")
	headers.Set("X-Custom", "test-value")

	rep.ReplicateAsync(WriteRecord{
		Method:  http.MethodPost,
		Path:    "/indexes/test/documents",
		Headers: headers,
		Body:    []byte(`[]`),
		TaskUID: 1,
	})

	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	ct := gotContentType
	custom := gotCustom
	mu.Unlock()

	if ct != "application/json" {
		t.Errorf("expected Content-Type application/json, got %q", ct)
	}
	if custom != "test-value" {
		t.Errorf("expected X-Custom test-value, got %q", custom)
	}
}

func TestReplicateAsync_ReplicaReturnsOK(t *testing.T) {
	// Some operations return 200 instead of 202 — should still count as success
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := &config.Config{
		Nodes: []config.NodeConfig{
			{URL: "http://primary:7700", Role: "primary"},
			{URL: srv.URL, Role: "replica"},
		},
		HealthCheck: config.HealthConfig{
			Interval: 5e9, Timeout: 2e9, UnhealthyThreshold: 3, HealthyThreshold: 2,
		},
	}
	checker := health.NewChecker(cfg)
	rep := New(checker, 5*time.Second)

	rep.ReplicateAsync(WriteRecord{
		Method:  http.MethodDelete,
		Path:    "/indexes/movies",
		Body:    nil,
		TaskUID: 10,
	})

	time.Sleep(200 * time.Millisecond)

	success, failure := rep.Stats()
	if success != 1 {
		t.Errorf("expected 1 success for 200 response, got %d", success)
	}
	if failure != 0 {
		t.Errorf("expected 0 failures, got %d", failure)
	}
}

func TestReplicateAsync_ReplicaLagTracking(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	cfg := &config.Config{
		Nodes: []config.NodeConfig{
			{URL: "http://primary:7700", Role: "primary"},
			{URL: srv.URL, Role: "replica"},
		},
		HealthCheck: config.HealthConfig{
			Interval: 5e9, Timeout: 2e9, UnhealthyThreshold: 3, HealthyThreshold: 2,
		},
	}
	checker := health.NewChecker(cfg)
	rep := New(checker, 5*time.Second)

	// Replicate with taskUID 50
	rep.ReplicateAsync(WriteRecord{
		Method:  http.MethodPost,
		Path:    "/indexes/test/documents",
		Body:    []byte(`[]`),
		TaskUID: 50,
	})
	time.Sleep(200 * time.Millisecond)

	lag := rep.ReplicaLag()
	if lag[srv.URL] != 50 {
		t.Errorf("expected lag 50, got %d", lag[srv.URL])
	}

	// Replicate with lower taskUID — should NOT decrease lag
	rep.ReplicateAsync(WriteRecord{
		Method:  http.MethodPost,
		Path:    "/indexes/test/documents",
		Body:    []byte(`[]`),
		TaskUID: 30,
	})
	time.Sleep(200 * time.Millisecond)

	lag = rep.ReplicaLag()
	if lag[srv.URL] != 50 {
		t.Errorf("expected lag still 50 after lower taskUID, got %d", lag[srv.URL])
	}
}

func TestCaptureWriteFromReader_ReadError(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/indexes/test/documents", strings.NewReader("test"))
	// Pass a reader that errors
	captured, err := CaptureWriteFromReader(req, &errorReader{})
	if err == nil {
		t.Error("expected error from erroring reader")
	}
	if captured != nil {
		t.Errorf("expected nil captured body on error, got %v", captured)
	}
}

type errorReader struct{}

func (e *errorReader) Read(_ []byte) (int, error) {
	return 0, fmt.Errorf("simulated read error")
}

func TestCaptureWrite_EmptyBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/indexes/test/documents", strings.NewReader(""))
	captured, err := CaptureWrite(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(captured) != 0 {
		t.Errorf("expected empty body, got %v", captured)
	}
}

func TestCaptureWrite_TrueNilBody(t *testing.T) {
	// httptest.NewRequest with nil sets Body to http.NoBody, not nil.
	// This tests the actual nil Body path.
	req := httptest.NewRequest(http.MethodDelete, "/indexes/movies", nil)
	req.Body = nil // force true nil
	captured, err := CaptureWrite(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if captured != nil {
		t.Errorf("expected nil captured for nil body, got %v", captured)
	}
}

func TestCaptureWriteFromReader_EmptyBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/indexes/test/documents", strings.NewReader(""))
	captured, err := CaptureWriteFromReader(req, strings.NewReader(""))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(captured) != 0 {
		t.Errorf("expected empty body, got %v", captured)
	}
}

func TestCaptureWrite_ReadError(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/indexes/test/documents", nil)
	req.Body = io.NopCloser(&errorReader{})
	_, err := CaptureWrite(req)
	if err == nil {
		t.Error("expected error from erroring body reader")
	}
}

func TestReplicateAsync_InvalidNodeURL(t *testing.T) {
	// A node with a URL that makes http.NewRequestWithContext fail
	cfg := &config.Config{
		Nodes: []config.NodeConfig{
			{URL: "http://primary:7700", Role: "primary"},
			{URL: "http://replica\x00:7700", Role: "replica"}, // null byte in URL
		},
		HealthCheck: config.HealthConfig{
			Interval: 5e9, Timeout: 2e9, UnhealthyThreshold: 3, HealthyThreshold: 2,
		},
	}
	checker := health.NewChecker(cfg)
	rep := New(checker, 1*time.Second)

	// Should not panic — the error is logged and counted as failure
	rep.ReplicateAsync(WriteRecord{
		Method:  http.MethodPost,
		Path:    "/indexes/test/documents",
		Body:    []byte(`[]`),
		TaskUID: 1,
	})

	time.Sleep(200 * time.Millisecond)

	_, failure := rep.Stats()
	if failure != 1 {
		t.Errorf("expected 1 failure for invalid URL, got %d", failure)
	}
}

func TestCaptureWriteFromReader_TrueNilBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodDelete, "/indexes/movies", nil)
	req.Body = nil // force true nil
	captured, err := CaptureWriteFromReader(req, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if captured != nil {
		t.Errorf("expected nil for true nil body, got %v", captured)
	}
}
