package proxy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/a-safe-digital/meilisearch-ha-proxy/internal/config"
	"github.com/a-safe-digital/meilisearch-ha-proxy/internal/health"
	"github.com/a-safe-digital/meilisearch-ha-proxy/internal/replication"
)

func TestMaxPayloadSize_RejectsLargeContentLength(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("backend should not receive oversized request")
	}))
	defer srv.Close()

	checker := makeChecker(srv.URL)
	proxy := New(checker, WithMaxPayloadSize(100)) // 100 bytes max
	proxy.SetReplicator(replication.New(checker, 5e9))

	body := strings.Repeat("x", 200)
	req := httptest.NewRequest(http.MethodPost, "/indexes/movies/documents", strings.NewReader(body))
	req.Header.Set("Content-Length", "200")
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("expected 413, got %d", w.Code)
	}
}

func TestMaxPayloadSize_AllowsSmallPayload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"taskUid":1,"status":"enqueued"}`))
	}))
	defer srv.Close()

	checker := makeChecker(srv.URL)
	proxy := New(checker, WithMaxPayloadSize(1024))
	proxy.SetReplicator(replication.New(checker, 5e9))

	body := `[{"id":1,"title":"Short"}]`
	req := httptest.NewRequest(http.MethodPost, "/indexes/movies/documents", strings.NewReader(body))
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Errorf("expected 202, got %d, body: %s", w.Code, w.Body.String())
	}
}

func TestMaxPayloadSize_ZeroMeansUnlimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"taskUid":1,"status":"enqueued"}`))
	}))
	defer srv.Close()

	checker := makeChecker(srv.URL)
	proxy := New(checker, WithMaxPayloadSize(0))

	body := strings.Repeat("x", 10000)
	req := httptest.NewRequest(http.MethodPost, "/indexes/movies/documents", strings.NewReader(body))
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Errorf("expected 202 (unlimited), got %d", w.Code)
	}
}

func TestMaxPayloadSize_RejectsStreamedLargeBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("backend should not receive oversized request")
	}))
	defer srv.Close()

	checker := makeChecker(srv.URL)
	proxy := New(checker, WithMaxPayloadSize(50))
	proxy.SetReplicator(replication.New(checker, 5e9))

	// No Content-Length header — body must be read to determine size
	body := strings.Repeat("x", 100)
	req := httptest.NewRequest(http.MethodPost, "/indexes/movies/documents", strings.NewReader(body))
	req.ContentLength = -1 // unknown content length
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("expected 413, got %d", w.Code)
	}
}

func TestWriteWithReplication_202(t *testing.T) {
	var primaryHit bool
	primarySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		primaryHit = true
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"taskUid":42,"status":"enqueued"}`))
	}))
	defer primarySrv.Close()

	replicaSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("TaskId") != "42" {
			t.Errorf("expected TaskId header '42', got %q", r.Header.Get("TaskId"))
		}
		w.WriteHeader(http.StatusAccepted)
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
	rep := replication.New(checker, 5e9)

	proxy := New(checker)
	proxy.SetReplicator(rep)

	req := httptest.NewRequest(http.MethodPost, "/indexes/movies/documents", strings.NewReader(`[{"id":1}]`))
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, req)

	if !primaryHit {
		t.Error("expected primary to be hit")
	}
	if w.Code != http.StatusAccepted {
		t.Errorf("expected 202, got %d", w.Code)
	}
}

func TestWriteNon202_NoReplication(t *testing.T) {
	primarySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"invalid","code":"bad_request"}`))
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
	proxy := New(checker)
	proxy.SetReplicator(replication.New(checker, 5e9))

	req := httptest.NewRequest(http.MethodPost, "/indexes/movies/documents", strings.NewReader(`invalid`))
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestAdminFallbackToNil(t *testing.T) {
	checker := makeChecker("http://nowhere:7700")
	proxy := New(checker)
	// No admin handler set, primary is unreachable

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, req)

	// Should try forwardToPrimary which forwards to unreachable host → 502 Bad Gateway
	if w.Code != http.StatusBadGateway {
		t.Errorf("expected 502 when no admin handler and unreachable primary, got %d", w.Code)
	}
}

func makeChecker(primaryURL string) *health.Checker {
	return health.NewChecker(&config.Config{
		Nodes: []config.NodeConfig{
			{URL: primaryURL, Role: "primary"},
		},
		HealthCheck: config.HealthConfig{
			Interval: 5e9, Timeout: 2e9, UnhealthyThreshold: 3, HealthyThreshold: 2,
		},
	})
}
