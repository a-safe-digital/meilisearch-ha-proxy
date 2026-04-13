package replication

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/a-safe-digital/meilisearch-ha-proxy/internal/config"
	"github.com/a-safe-digital/meilisearch-ha-proxy/internal/health"
)

func TestReplicateAsync_SendsToReplicas(t *testing.T) {
	var replicaHits atomic.Int32
	var gotTaskID string
	var mu sync.Mutex

	replicaSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		replicaHits.Add(1)
		mu.Lock()
		gotTaskID = r.Header.Get("TaskId")
		mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"taskUid":42}`))
	}))
	defer replicaSrv.Close()

	cfg := &config.Config{
		Nodes: []config.NodeConfig{
			{URL: "http://primary:7700", Role: "primary"},
			{URL: replicaSrv.URL, APIKey: "rep-key", Role: "replica"},
		},
		HealthCheck: config.HealthConfig{
			Interval:           5e9,
			Timeout:            2e9,
			UnhealthyThreshold: 3,
			HealthyThreshold:   2,
		},
	}

	checker := health.NewChecker(cfg)
	rep := New(checker, 5*time.Second)

	rec := WriteRecord{
		Method:  http.MethodPost,
		Path:    "/indexes/movies/documents",
		Headers: http.Header{"Content-Type": []string{"application/json"}},
		Body:    []byte(`[{"id":1,"title":"Test"}]`),
		TaskUID: 42,
	}

	rep.ReplicateAsync(rec)

	// Wait for async replication
	time.Sleep(200 * time.Millisecond)

	if replicaHits.Load() != 1 {
		t.Errorf("expected 1 replica hit, got %d", replicaHits.Load())
	}

	mu.Lock()
	if gotTaskID != "42" {
		t.Errorf("expected TaskId header '42', got %q", gotTaskID)
	}
	mu.Unlock()

	success, failure := rep.Stats()
	if success != 1 {
		t.Errorf("expected 1 success, got %d", success)
	}
	if failure != 0 {
		t.Errorf("expected 0 failures, got %d", failure)
	}
}

func TestReplicateAsync_MultipleReplicas(t *testing.T) {
	var hits atomic.Int32

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusAccepted)
	})

	srv1 := httptest.NewServer(handler)
	defer srv1.Close()
	srv2 := httptest.NewServer(handler)
	defer srv2.Close()

	cfg := &config.Config{
		Nodes: []config.NodeConfig{
			{URL: "http://primary:7700", Role: "primary"},
			{URL: srv1.URL, Role: "replica"},
			{URL: srv2.URL, Role: "replica"},
		},
		HealthCheck: config.HealthConfig{
			Interval:           5e9,
			Timeout:            2e9,
			UnhealthyThreshold: 3,
			HealthyThreshold:   2,
		},
	}

	checker := health.NewChecker(cfg)
	rep := New(checker, 5*time.Second)

	rec := WriteRecord{
		Method:  http.MethodPost,
		Path:    "/indexes/movies/documents",
		Body:    []byte(`[{"id":1}]`),
		TaskUID: 10,
	}

	rep.ReplicateAsync(rec)
	time.Sleep(200 * time.Millisecond)

	if hits.Load() != 2 {
		t.Errorf("expected 2 replica hits, got %d", hits.Load())
	}
}

func TestReplicateAsync_ReplicaDown(t *testing.T) {
	cfg := &config.Config{
		Nodes: []config.NodeConfig{
			{URL: "http://primary:7700", Role: "primary"},
			{URL: "http://127.0.0.1:19998", Role: "replica"},
		},
		HealthCheck: config.HealthConfig{
			Interval:           5e9,
			Timeout:            2e9,
			UnhealthyThreshold: 3,
			HealthyThreshold:   2,
		},
	}

	checker := health.NewChecker(cfg)
	rep := New(checker, 1*time.Second)

	rec := WriteRecord{
		Method:  http.MethodPost,
		Path:    "/indexes/movies/documents",
		Body:    []byte(`[{"id":1}]`),
		TaskUID: 5,
	}

	rep.ReplicateAsync(rec)
	time.Sleep(1500 * time.Millisecond)

	_, failure := rep.Stats()
	if failure != 1 {
		t.Errorf("expected 1 failure, got %d", failure)
	}
}

func TestReplicateAsync_ReplicaReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfg := &config.Config{
		Nodes: []config.NodeConfig{
			{URL: "http://primary:7700", Role: "primary"},
			{URL: srv.URL, Role: "replica"},
		},
		HealthCheck: config.HealthConfig{
			Interval:           5e9,
			Timeout:            2e9,
			UnhealthyThreshold: 3,
			HealthyThreshold:   2,
		},
	}

	checker := health.NewChecker(cfg)
	rep := New(checker, 5*time.Second)

	rec := WriteRecord{
		Method:  http.MethodPost,
		Path:    "/indexes/movies/documents",
		Body:    []byte(`[{"id":1}]`),
		TaskUID: 7,
	}

	rep.ReplicateAsync(rec)
	time.Sleep(200 * time.Millisecond)

	_, failure := rep.Stats()
	if failure != 1 {
		t.Errorf("expected 1 failure, got %d", failure)
	}
}

func TestReplicaLag(t *testing.T) {
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
			Interval:           5e9,
			Timeout:            2e9,
			UnhealthyThreshold: 3,
			HealthyThreshold:   2,
		},
	}

	checker := health.NewChecker(cfg)
	rep := New(checker, 5*time.Second)

	// Replicate 3 writes
	for i := int64(1); i <= 3; i++ {
		rep.ReplicateAsync(WriteRecord{
			Method:  http.MethodPost,
			Path:    "/indexes/movies/documents",
			Body:    []byte(`[{"id":1}]`),
			TaskUID: i,
		})
	}

	time.Sleep(300 * time.Millisecond)

	lag := rep.ReplicaLag()
	if lag[srv.URL] != 3 {
		t.Errorf("expected replica lag taskUid=3, got %d", lag[srv.URL])
	}
}

func TestReplicateAsync_SetsAuthHeader(t *testing.T) {
	var mu sync.Mutex
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotAuth = r.Header.Get("Authorization")
		mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	cfg := &config.Config{
		Nodes: []config.NodeConfig{
			{URL: "http://primary:7700", Role: "primary"},
			{URL: srv.URL, APIKey: "replica-key", Role: "replica"},
		},
		HealthCheck: config.HealthConfig{
			Interval:           5e9,
			Timeout:            2e9,
			UnhealthyThreshold: 3,
			HealthyThreshold:   2,
		},
	}

	checker := health.NewChecker(cfg)
	rep := New(checker, 5*time.Second)

	rep.ReplicateAsync(WriteRecord{
		Method:  http.MethodPost,
		Path:    "/indexes/test/documents",
		Body:    []byte(`[]`),
		TaskUID: 1,
	})

	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	auth := gotAuth
	mu.Unlock()
	if auth != "Bearer replica-key" {
		t.Errorf("expected 'Bearer replica-key', got %q", auth)
	}
}

func TestCaptureWrite(t *testing.T) {
	body := `[{"id":1,"title":"Test"}]`
	req := httptest.NewRequest(http.MethodPost, "/indexes/movies/documents", strings.NewReader(body))

	captured, err := CaptureWrite(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(captured) != body {
		t.Errorf("expected %q, got %q", body, string(captured))
	}

	// Verify the request body is still readable (was replaced with NopCloser)
	buf := make([]byte, len(body))
	n, _ := req.Body.Read(buf)
	if string(buf[:n]) != body {
		t.Errorf("body not re-readable, got %q", string(buf[:n]))
	}
}

func TestCaptureWrite_NilBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodDelete, "/indexes/movies", nil)
	captured, err := CaptureWrite(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(captured) != 0 {
		t.Errorf("expected empty body, got %v", captured)
	}
}
