package proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/a-safe-digital/meilisearch-ha-proxy/internal/config"
	"github.com/a-safe-digital/meilisearch-ha-proxy/internal/health"
	"github.com/a-safe-digital/meilisearch-ha-proxy/internal/replication"
)

func TestSetFallback(t *testing.T) {
	checker := makeTestChecker("http://primary:7700")
	admin := NewAdminHandler(checker, nil)

	called := false
	admin.SetFallback(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	// Request an admin path that falls through to fallback (e.g., /tasks)
	req := httptest.NewRequest(http.MethodGet, "/tasks", nil)
	w := httptest.NewRecorder()
	admin.ServeHTTP(w, req)

	if !called {
		t.Error("expected fallback to be called")
	}
}

func TestAdminNoFallback_Returns404(t *testing.T) {
	checker := makeTestChecker("http://primary:7700")
	admin := NewAdminHandler(checker, nil)
	// No fallback set

	req := httptest.NewRequest(http.MethodGet, "/tasks", nil)
	w := httptest.NewRecorder()
	admin.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestClusterStatus_WithReplicator(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := &config.Config{
		Nodes: []config.NodeConfig{
			{URL: srv.URL, Role: "primary"},
			{URL: "http://replica:7700", Role: "replica"},
		},
		HealthCheck: config.HealthConfig{
			Interval: 5e9, Timeout: 2e9, UnhealthyThreshold: 3, HealthyThreshold: 2,
		},
	}
	checker := health.NewChecker(cfg)
	rep := replication.New(checker, 5e9)

	admin := NewAdminHandler(checker, rep)

	req := httptest.NewRequest(http.MethodGet, "/cluster/status", nil)
	w := httptest.NewRecorder()
	admin.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var status ClusterStatus
	if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
		t.Fatalf("json decode: %v", err)
	}
	if len(status.Nodes) != 2 {
		t.Errorf("expected 2 nodes, got %d", len(status.Nodes))
	}
}

func TestSetAdminHandler_ConfiguresFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	defer srv.Close()

	checker := makeTestChecker(srv.URL)
	proxy := New(checker)
	admin := NewAdminHandler(checker, nil)
	proxy.SetAdminHandler(admin)

	// /tasks should be forwarded to primary via the fallback
	req := httptest.NewRequest(http.MethodGet, "/tasks", nil)
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 (forwarded to primary), got %d", w.Code)
	}
}

func TestClassifierRequestTypeString(t *testing.T) {
	tests := []struct {
		rt   RequestType
		want string
	}{
		{ReadRequest, "read"},
		{WriteRequest, "write"},
		{AdminRequest, "admin"},
		{RequestType(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.rt.String(); got != tt.want {
			t.Errorf("RequestType(%d).String() = %q, want %q", int(tt.rt), got, tt.want)
		}
	}
}

func TestClassifier_HeadMethod(t *testing.T) {
	req := httptest.NewRequest(http.MethodHead, "/indexes/movies/documents", nil)
	if rt := Classify(req); rt != ReadRequest {
		t.Errorf("HEAD should be ReadRequest, got %s", rt)
	}
}

func TestClassifier_OptionsMethod(t *testing.T) {
	req := httptest.NewRequest(http.MethodOptions, "/indexes/movies/documents", nil)
	if rt := Classify(req); rt != ReadRequest {
		t.Errorf("OPTIONS should be ReadRequest, got %s", rt)
	}
}

func TestClassifier_TrailingSlash(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/health/", nil)
	if rt := Classify(req); rt != AdminRequest {
		t.Errorf("/health/ should be AdminRequest, got %s", rt)
	}
}

func TestClassifier_KeysSubpath(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/keys/abc123", nil)
	if rt := Classify(req); rt != AdminRequest {
		t.Errorf("/keys/abc123 should be AdminRequest, got %s", rt)
	}
}

func TestClassifier_TasksSubpath(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/tasks/42", nil)
	if rt := Classify(req); rt != AdminRequest {
		t.Errorf("/tasks/42 should be AdminRequest, got %s", rt)
	}
}

func TestCleanPath_Root(t *testing.T) {
	got := cleanPath("/")
	if got != "/" {
		t.Errorf("cleanPath('/') = %q, want '/'", got)
	}
}

func TestCleanPath_SingleSlash(t *testing.T) {
	got := cleanPath("/test/")
	if got != "/test" {
		t.Errorf("cleanPath('/test/') = %q, want '/test'", got)
	}
}

func makeTestChecker(primaryURL string) *health.Checker {
	return health.NewChecker(&config.Config{
		Nodes: []config.NodeConfig{
			{URL: primaryURL, Role: "primary"},
		},
		HealthCheck: config.HealthConfig{
			Interval: 5e9, Timeout: 2e9, UnhealthyThreshold: 3, HealthyThreshold: 2,
		},
	})
}
