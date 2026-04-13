package proxy

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/a-safe-digital/meilisearch-ha-proxy/internal/config"
	"github.com/a-safe-digital/meilisearch-ha-proxy/internal/health"
)

func TestReadRoundRobin(t *testing.T) {
	// Create 3 backend servers
	var hits [3]int
	servers := make([]*httptest.Server, 3)
	for i := range servers {
		idx := i
		servers[i] = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			hits[idx]++
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"hits":[]}`))
		}))
		defer servers[i].Close()
	}

	cfg := &config.Config{
		Nodes: []config.NodeConfig{
			{URL: servers[0].URL, Role: "primary"},
			{URL: servers[1].URL, Role: "replica"},
			{URL: servers[2].URL, Role: "replica"},
		},
		HealthCheck: config.HealthConfig{
			Interval:           5e9,
			Timeout:            2e9,
			UnhealthyThreshold: 3,
			HealthyThreshold:   2,
		},
	}

	checker := health.NewChecker(cfg)
	proxy := New(checker)

	// Send 9 search requests — should round-robin across all 3 nodes
	for i := 0; i < 9; i++ {
		req := httptest.NewRequest(http.MethodPost, "/indexes/movies/search", strings.NewReader(`{"q":"test"}`))
		w := httptest.NewRecorder()
		proxy.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("request %d: expected 200, got %d", i, w.Code)
		}
	}

	// Each server should have been hit 3 times
	for i, h := range hits {
		if h != 3 {
			t.Errorf("server %d: expected 3 hits, got %d", i, h)
		}
	}
}

func TestReadSkipsUnhealthyNodes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"hits":[]}`))
	}))
	defer srv.Close()

	cfg := &config.Config{
		Nodes: []config.NodeConfig{
			{URL: "http://dead-node:7700", Role: "primary"},
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
	// Drive primary to unhealthy
	nodes := checker.Nodes()
	for i := 0; i < 3; i++ {
		nodes[0].Check(context.Background(), 0) // will fail — nil context
	}

	proxy := New(checker)

	req := httptest.NewRequest(http.MethodPost, "/indexes/movies/search", strings.NewReader(`{"q":"test"}`))
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, req)

	// Should still work via the healthy replica
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d, body: %s", w.Code, w.Body.String())
	}
}

func TestReadNoHealthyNodes(t *testing.T) {
	cfg := &config.Config{
		Nodes: []config.NodeConfig{
			{URL: "http://dead:7700", Role: "primary"},
		},
		HealthCheck: config.HealthConfig{
			Interval:           5e9,
			Timeout:            2e9,
			UnhealthyThreshold: 2,
			HealthyThreshold:   2,
		},
	}

	checker := health.NewChecker(cfg)
	// Drive node to unhealthy
	nodes := checker.Nodes()
	for i := 0; i < 3; i++ {
		nodes[0].Check(context.Background(), 0)
	}

	proxy := New(checker)

	req := httptest.NewRequest(http.MethodPost, "/indexes/movies/search", strings.NewReader(`{"q":"test"}`))
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", w.Code)
	}
}

func TestWriteForwardsToPrimary(t *testing.T) {
	var primaryHit bool
	primarySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		primaryHit = true
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"taskUid":42,"status":"enqueued"}`))
	}))
	defer primarySrv.Close()

	replicaSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("replica should not receive writes directly")
	}))
	defer replicaSrv.Close()

	cfg := &config.Config{
		Nodes: []config.NodeConfig{
			{URL: primarySrv.URL, Role: "primary"},
			{URL: replicaSrv.URL, Role: "replica"},
		},
		HealthCheck: config.HealthConfig{
			Interval:           5e9,
			Timeout:            2e9,
			UnhealthyThreshold: 3,
			HealthyThreshold:   2,
		},
	}

	checker := health.NewChecker(cfg)
	proxy := New(checker)

	req := httptest.NewRequest(http.MethodPost, "/indexes/movies/documents", strings.NewReader(`[{"id":1,"title":"Test"}]`))
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, req)

	if !primaryHit {
		t.Error("expected primary to receive the write")
	}
	if w.Code != http.StatusAccepted {
		t.Errorf("expected 202, got %d", w.Code)
	}
}

func TestWriteUnhealthyPrimary(t *testing.T) {
	cfg := &config.Config{
		Nodes: []config.NodeConfig{
			{URL: "http://dead:7700", Role: "primary"},
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
	for i := 0; i < 3; i++ {
		nodes[0].Check(context.Background(), 0)
	}

	proxy := New(checker)

	req := httptest.NewRequest(http.MethodPost, "/indexes/movies/documents", strings.NewReader(`[{"id":1}]`))
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", w.Code)
	}
}

func TestResponseHeadersForwarded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Custom-Header", "hello")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"hits":[]}`))
	}))
	defer srv.Close()

	cfg := &config.Config{
		Nodes: []config.NodeConfig{
			{URL: srv.URL, Role: "primary"},
		},
		HealthCheck: config.HealthConfig{
			Interval:           5e9,
			Timeout:            2e9,
			UnhealthyThreshold: 3,
			HealthyThreshold:   2,
		},
	}

	checker := health.NewChecker(cfg)
	proxy := New(checker)

	req := httptest.NewRequest(http.MethodGet, "/indexes/movies/documents", nil)
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, req)

	if w.Header().Get("X-Custom-Header") != "hello" {
		t.Errorf("expected X-Custom-Header: hello, got %q", w.Header().Get("X-Custom-Header"))
	}
}

func TestRequestHeadersForwarded(t *testing.T) {
	var gotContentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := &config.Config{
		Nodes: []config.NodeConfig{
			{URL: srv.URL, Role: "primary"},
		},
		HealthCheck: config.HealthConfig{
			Interval:           5e9,
			Timeout:            2e9,
			UnhealthyThreshold: 3,
			HealthyThreshold:   2,
		},
	}

	checker := health.NewChecker(cfg)
	proxy := New(checker)

	req := httptest.NewRequest(http.MethodPost, "/indexes/movies/search", strings.NewReader(`{"q":"test"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, req)

	if gotContentType != "application/json" {
		t.Errorf("expected Content-Type: application/json, got %q", gotContentType)
	}
}

func TestResponseBodyForwarded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"hits":[{"id":1,"title":"Test Movie"}]}`))
	}))
	defer srv.Close()

	cfg := &config.Config{
		Nodes: []config.NodeConfig{
			{URL: srv.URL, Role: "primary"},
		},
		HealthCheck: config.HealthConfig{
			Interval:           5e9,
			Timeout:            2e9,
			UnhealthyThreshold: 3,
			HealthyThreshold:   2,
		},
	}

	checker := health.NewChecker(cfg)
	proxy := New(checker)

	req := httptest.NewRequest(http.MethodPost, "/indexes/movies/search", strings.NewReader(`{"q":"test"}`))
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, req)

	body, _ := io.ReadAll(w.Body)
	expected := `{"hits":[{"id":1,"title":"Test Movie"}]}`
	if string(body) != expected {
		t.Errorf("expected %q, got %q", expected, string(body))
	}
}
