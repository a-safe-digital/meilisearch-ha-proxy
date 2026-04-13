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

func TestHandleWrite_NoPrimary(t *testing.T) {
	// Config with only replicas, no primary
	cfg := &config.Config{
		Nodes: []config.NodeConfig{
			{URL: "http://meili-1:7700", Role: "replica"},
		},
		HealthCheck: config.HealthConfig{
			Interval: 5e9, Timeout: 2e9, UnhealthyThreshold: 3, HealthyThreshold: 2,
		},
	}
	checker := health.NewChecker(cfg)
	p := New(checker)

	req := httptest.NewRequest(http.MethodPost, "/indexes/movies/documents", strings.NewReader(`[{"id":1}]`))
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 when no primary, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "no primary") {
		t.Errorf("expected error message about no primary, got %q", w.Body.String())
	}
}

func TestForwardToPrimary_NoPrimary(t *testing.T) {
	cfg := &config.Config{
		Nodes: []config.NodeConfig{
			{URL: "http://meili-1:7700", Role: "replica"},
		},
		HealthCheck: config.HealthConfig{
			Interval: 5e9, Timeout: 2e9, UnhealthyThreshold: 3, HealthyThreshold: 2,
		},
	}
	checker := health.NewChecker(cfg)
	p := New(checker)
	// No admin handler — handleAdmin will call forwardToPrimary

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 when no primary for admin forward, got %d", w.Code)
	}
}

func TestForwardRequest_InvalidNodeURL(t *testing.T) {
	cfg := &config.Config{
		Nodes: []config.NodeConfig{
			{URL: "://invalid-url", Role: "primary"},
		},
		HealthCheck: config.HealthConfig{
			Interval: 5e9, Timeout: 2e9, UnhealthyThreshold: 3, HealthyThreshold: 2,
		},
	}
	checker := health.NewChecker(cfg)
	p := New(checker)

	req := httptest.NewRequest(http.MethodGet, "/indexes/test", nil)
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 for invalid node URL, got %d", w.Code)
	}
}

func TestProxy_SetAdminHandler_NonAdminHandler(t *testing.T) {
	// SetAdminHandler with a regular http.Handler (not *AdminHandler)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	checker := makeChecker(srv.URL)
	p := New(checker)

	called := false
	p.SetAdminHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)

	if !called {
		t.Error("expected custom admin handler to be called")
	}
}

func TestProxy_AuthHeaderSetForBackend(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := &config.Config{
		Nodes: []config.NodeConfig{
			{URL: srv.URL, APIKey: "my-secret-key", Role: "primary"},
		},
		HealthCheck: config.HealthConfig{
			Interval: 5e9, Timeout: 2e9, UnhealthyThreshold: 3, HealthyThreshold: 2,
		},
	}
	checker := health.NewChecker(cfg)
	p := New(checker)

	req := httptest.NewRequest(http.MethodGet, "/indexes/test", nil)
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)

	if gotAuth != "Bearer my-secret-key" {
		t.Errorf("expected 'Bearer my-secret-key', got %q", gotAuth)
	}
}

func TestProxy_WriteWithReplicator_Non202(t *testing.T) {
	// Primary returns 200 (not 202) — replicator should NOT replicate
	primarySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	defer primarySrv.Close()

	replicaSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("replica should not be hit for non-202 response")
	}))
	defer replicaSrv.Close()

	cfg := &config.Config{
		Nodes: []config.NodeConfig{
			{URL: primarySrv.URL, Role: "primary"},
			{URL: replicaSrv.URL, Role: "replica"},
		},
		HealthCheck: config.HealthConfig{
			Interval: 5e9, Timeout: 2e9, UnhealthyThreshold: 3, HealthyThreshold: 2,
		},
	}
	checker := health.NewChecker(cfg)
	rep := replication.New(checker, 5*time.Second)
	p := New(checker)
	p.SetReplicator(rep)

	req := httptest.NewRequest(http.MethodDelete, "/indexes/movies", nil)
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	// Wait a bit to ensure no replication happens
	time.Sleep(100 * time.Millisecond)
	success, _ := rep.Stats()
	if success != 0 {
		t.Errorf("expected 0 replication successes for non-202, got %d", success)
	}
}

func TestProxy_WriteWith202_InvalidTaskUidResponse(t *testing.T) {
	// Primary returns 202 but body has no valid taskUid
	primarySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"invalid":"response"}`))
	}))
	defer primarySrv.Close()

	cfg := &config.Config{
		Nodes: []config.NodeConfig{
			{URL: primarySrv.URL, Role: "primary"},
			{URL: "http://replica:7700", Role: "replica"},
		},
		HealthCheck: config.HealthConfig{
			Interval: 5e9, Timeout: 2e9, UnhealthyThreshold: 3, HealthyThreshold: 2,
		},
	}
	checker := health.NewChecker(cfg)
	rep := replication.New(checker, 5*time.Second)
	p := New(checker)
	p.SetReplicator(rep)

	req := httptest.NewRequest(http.MethodPost, "/indexes/movies/documents", strings.NewReader(`[{"id":1}]`))
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)

	// Client should still get 202
	if w.Code != http.StatusAccepted {
		t.Errorf("expected 202, got %d", w.Code)
	}
}

func TestProxy_handleAdmin_WithAdminHandler(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	checker := makeChecker(srv.URL)
	admin := NewAdminHandler(checker, nil)
	p := New(checker)
	p.SetAdminHandler(admin)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestResponseRecorder_DefaultStatusCode(t *testing.T) {
	rr := &responseRecorder{ResponseWriter: httptest.NewRecorder()}

	// Write without calling WriteHeader first
	_, _ = rr.Write([]byte("hello"))

	if rr.statusCode != http.StatusOK {
		t.Errorf("expected default status 200, got %d", rr.statusCode)
	}
	if string(rr.body) != "hello" {
		t.Errorf("expected body 'hello', got %q", string(rr.body))
	}
}

func TestResponseRecorder_ExplicitStatusCode(t *testing.T) {
	rr := &responseRecorder{ResponseWriter: httptest.NewRecorder()}
	rr.WriteHeader(http.StatusNotFound)

	if rr.statusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rr.statusCode)
	}
}

func TestWriteWithReplicator_BackendUnavailable(t *testing.T) {
	// Primary is healthy but backend goes away mid-request
	cfg := &config.Config{
		Nodes: []config.NodeConfig{
			{URL: "http://127.0.0.1:19998", Role: "primary"},
		},
		HealthCheck: config.HealthConfig{
			Interval: 5e9, Timeout: 2e9, UnhealthyThreshold: 3, HealthyThreshold: 2,
		},
	}
	checker := health.NewChecker(cfg)
	rep := replication.New(checker, 1*time.Second)
	p := New(checker)
	p.SetReplicator(rep)

	req := httptest.NewRequest(http.MethodPost, "/indexes/movies/documents", strings.NewReader(`[{"id":1}]`))
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)

	// Should get 502 because primary is unreachable
	if w.Code != http.StatusBadGateway {
		t.Errorf("expected 502 when primary is unreachable, got %d", w.Code)
	}
}

func TestClassifier_ClusterStatus(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/cluster/status", nil)
	if rt := Classify(req); rt != AdminRequest {
		t.Errorf("/cluster/status should be AdminRequest, got %s", rt)
	}
}

func TestClassifier_DeleteTasks(t *testing.T) {
	req := httptest.NewRequest(http.MethodDelete, "/tasks", nil)
	if rt := Classify(req); rt != AdminRequest {
		t.Errorf("DELETE /tasks should be AdminRequest, got %s", rt)
	}
}

func TestClassifier_PostTasksCancel(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/tasks/cancel", nil)
	if rt := Classify(req); rt != AdminRequest {
		t.Errorf("POST /tasks/cancel should be AdminRequest, got %s", rt)
	}
}

func TestClassifier_VersionEndpoint(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/version", nil)
	if rt := Classify(req); rt != AdminRequest {
		t.Errorf("/version should be AdminRequest, got %s", rt)
	}
}

func TestClassifier_StatsEndpoint(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/stats", nil)
	if rt := Classify(req); rt != AdminRequest {
		t.Errorf("/stats should be AdminRequest, got %s", rt)
	}
}

func TestAdmin_HealthNoPrimary(t *testing.T) {
	cfg := &config.Config{
		Nodes: []config.NodeConfig{
			{URL: "http://meili-1:7700", Role: "replica"},
		},
		HealthCheck: config.HealthConfig{
			Interval: 5e9, Timeout: 2e9, UnhealthyThreshold: 3, HealthyThreshold: 2,
		},
	}
	checker := health.NewChecker(cfg)
	admin := NewAdminHandler(checker, nil)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	admin.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 with no primary, got %d", w.Code)
	}
}

func TestAdmin_ClusterStatus_NoReplicator(t *testing.T) {
	cfg := &config.Config{
		Nodes: []config.NodeConfig{
			{URL: "http://meili-0:7700", Role: "primary"},
		},
		HealthCheck: config.HealthConfig{
			Interval: 5e9, Timeout: 2e9, UnhealthyThreshold: 3, HealthyThreshold: 2,
		},
	}
	checker := health.NewChecker(cfg)
	admin := NewAdminHandler(checker, nil) // nil replicator

	req := httptest.NewRequest(http.MethodGet, "/cluster/status", nil)
	w := httptest.NewRecorder()
	admin.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestMaxPayloadSize_WriteWithoutReplicator(t *testing.T) {
	// Large Content-Length but no replicator — should still reject
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("backend should not receive oversized request")
	}))
	defer srv.Close()

	checker := makeChecker(srv.URL)
	p := New(checker, WithMaxPayloadSize(50))
	// No replicator set

	req := httptest.NewRequest(http.MethodPost, "/indexes/movies/documents", strings.NewReader(strings.Repeat("x", 100)))
	req.Header.Set("Content-Length", "100")
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("expected 413, got %d", w.Code)
	}
}

func TestMaxPayloadSize_SmallWithoutReplicator(t *testing.T) {
	// Small payload, no replicator, under limit — should forward
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"taskUid":1,"status":"enqueued"}`))
	}))
	defer srv.Close()

	checker := makeChecker(srv.URL)
	p := New(checker, WithMaxPayloadSize(1024))
	// No replicator

	req := httptest.NewRequest(http.MethodPost, "/indexes/movies/documents", strings.NewReader(`[{"id":1}]`))
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Errorf("expected 202, got %d", w.Code)
	}
}

func TestProxy_WriteWithReplicator_CaptureBodyError(t *testing.T) {
	// Test the capture body error path by passing a body that fails to read
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"taskUid":1,"status":"enqueued"}`))
	}))
	defer srv.Close()

	checker := makeChecker(srv.URL)
	rep := replication.New(checker, 5*time.Second)
	p := New(checker, WithMaxPayloadSize(10))
	p.SetReplicator(rep)

	// Create a request with a body that claims -1 content length but is actually larger than limit
	req := httptest.NewRequest(http.MethodPost, "/indexes/movies/documents", strings.NewReader(strings.Repeat("x", 20)))
	req.ContentLength = -1 // force streaming read
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)

	// Should get 413 since body exceeds max payload after streaming read
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("expected 413, got %d", w.Code)
	}
}

func TestProxy_WriteWithReplicator_BodyReadError(t *testing.T) {
	// Test the CaptureWriteFromReader error path in handleWrite
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("backend should not receive request with erroring body")
	}))
	defer srv.Close()

	checker := makeChecker(srv.URL)
	rep := replication.New(checker, 5*time.Second)
	p := New(checker)
	p.SetReplicator(rep)

	// Create request with a body that errors on read
	req := httptest.NewRequest(http.MethodPost, "/indexes/movies/documents", nil)
	req.Body = io.NopCloser(&failReader{})
	req.ContentLength = 10 // claim there's content
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 for body read error, got %d", w.Code)
	}
}

type failReader struct{}

func (f *failReader) Read(_ []byte) (int, error) {
	return 0, io.ErrUnexpectedEOF
}

func TestProxy_ForwardToPrimary_WithPrimary(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"available"}`))
	}))
	defer srv.Close()

	checker := makeChecker(srv.URL)
	p := New(checker)
	// No admin handler — admin requests fall through to forwardToPrimary

	req := httptest.NewRequest(http.MethodGet, "/version", nil)
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 from forwarded admin request, got %d", w.Code)
	}
}

func TestProxy_ReadRequestCancelledContext(t *testing.T) {
	// A slow backend with client cancellation
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(5 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	checker := makeChecker(srv.URL)
	p := New(checker)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	req := httptest.NewRequest(http.MethodGet, "/indexes/test", nil).WithContext(ctx)
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)

	// Should get 502 because the request timed out
	if w.Code != http.StatusBadGateway {
		t.Errorf("expected 502 for timed out request, got %d", w.Code)
	}
}
