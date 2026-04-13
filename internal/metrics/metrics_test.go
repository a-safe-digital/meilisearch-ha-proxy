package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRecordRequest(t *testing.T) {
	m := &Metrics{}
	// Replace global for this test
	old := global
	global = m
	defer func() { global = old }()

	m.RecordRequest("read")
	m.RecordRequest("read")
	m.RecordRequest("write")
	m.RecordRequest("admin")

	if m.readRequests.Load() != 2 {
		t.Errorf("expected 2 reads, got %d", m.readRequests.Load())
	}
	if m.writeRequests.Load() != 1 {
		t.Errorf("expected 1 write, got %d", m.writeRequests.Load())
	}
	if m.adminRequests.Load() != 1 {
		t.Errorf("expected 1 admin, got %d", m.adminRequests.Load())
	}
}

func TestRecordLatency(t *testing.T) {
	m := &Metrics{}
	old := global
	global = m
	defer func() { global = old }()

	m.RecordLatency("read", 100*time.Microsecond)
	m.RecordLatency("read", 200*time.Microsecond)
	m.RecordLatency("write", 500*time.Microsecond)

	if m.readCount.Load() != 2 {
		t.Errorf("expected read count 2, got %d", m.readCount.Load())
	}
	if m.readLatencySum.Load() != 300 {
		t.Errorf("expected read latency sum 300µs, got %d", m.readLatencySum.Load())
	}
	if m.writeCount.Load() != 1 {
		t.Errorf("expected write count 1, got %d", m.writeCount.Load())
	}
}

func TestRecordError(t *testing.T) {
	m := &Metrics{}
	old := global
	global = m
	defer func() { global = old }()

	m.RecordError("no_healthy_nodes")
	m.RecordError("bad_gateway")
	m.RecordError("bad_gateway")

	if m.errNoHealthy.Load() != 1 {
		t.Errorf("expected 1 no_healthy, got %d", m.errNoHealthy.Load())
	}
	if m.errBadGateway.Load() != 2 {
		t.Errorf("expected 2 bad_gateway, got %d", m.errBadGateway.Load())
	}
}

func TestSetNodeHealth(t *testing.T) {
	m := &Metrics{}
	old := global
	global = m
	defer func() { global = old }()

	m.SetNodeHealth("http://node1:7700", true)
	m.SetNodeHealth("http://node2:7700", false)

	v1, ok := m.nodeHealth.Load("http://node1:7700")
	if !ok || v1.(int) != 1 {
		t.Errorf("expected node1 healthy (1), got %v", v1)
	}
	v2, ok := m.nodeHealth.Load("http://node2:7700")
	if !ok || v2.(int) != 0 {
		t.Errorf("expected node2 unhealthy (0), got %v", v2)
	}
}

func TestRecordFailover(t *testing.T) {
	m := &Metrics{}
	old := global
	global = m
	defer func() { global = old }()

	m.RecordFailover()
	m.RecordFailover()

	if m.failoverTotal.Load() != 2 {
		t.Errorf("expected 2 failovers, got %d", m.failoverTotal.Load())
	}
}

func TestHandler_PrometheusFormat(t *testing.T) {
	m := &Metrics{}
	old := global
	global = m
	defer func() { global = old }()

	m.RecordRequest("read")
	m.RecordRequest("write")
	m.RecordError("bad_gateway")
	m.SetNodeHealth("http://meili-0:7700", true)
	m.SetNodeRole("http://meili-0:7700", "primary")
	m.SetReplicationStats(10, 2)
	m.RecordFailover()

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	w := httptest.NewRecorder()
	Handler().ServeHTTP(w, req)

	body := w.Body.String()

	checks := []string{
		`meili_ha_requests_total{type="read"} 1`,
		`meili_ha_requests_total{type="write"} 1`,
		`meili_ha_errors_total{type="bad_gateway"} 1`,
		`meili_ha_replication_total{status="success"} 10`,
		`meili_ha_replication_total{status="failure"} 2`,
		`meili_ha_failover_total 1`,
		`meili_ha_node_health{node="http://meili-0:7700",role="primary"} 1`,
	}

	for _, check := range checks {
		if !strings.Contains(body, check) {
			t.Errorf("expected metrics output to contain %q\ngot:\n%s", check, body)
		}
	}

	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "text/plain") {
		t.Errorf("expected text/plain content type, got %q", ct)
	}
}

func TestHandler_EmptyMetrics(t *testing.T) {
	m := &Metrics{}
	old := global
	global = m
	defer func() { global = old }()

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	w := httptest.NewRecorder()
	Handler().ServeHTTP(w, req)

	body := w.Body.String()
	if !strings.Contains(body, `meili_ha_requests_total{type="read"} 0`) {
		t.Errorf("expected zero counters in fresh metrics")
	}
}

func TestGet(t *testing.T) {
	m := Get()
	if m == nil {
		t.Fatal("Get() returned nil")
	}
	// Verify it's the global singleton
	if m != global {
		t.Error("Get() did not return global instance")
	}
}

func TestRecordRequest_UnknownType(t *testing.T) {
	m := &Metrics{}
	old := global
	global = m
	defer func() { global = old }()

	// Should not panic for unknown types
	m.RecordRequest("unknown")
	if m.readRequests.Load() != 0 && m.writeRequests.Load() != 0 && m.adminRequests.Load() != 0 {
		t.Error("unknown type should not increment any counter")
	}
}

func TestRecordLatency_UnknownType(t *testing.T) {
	m := &Metrics{}
	old := global
	global = m
	defer func() { global = old }()

	// Should not panic for unknown types
	m.RecordLatency("admin", 100*time.Microsecond)
	if m.readLatencySum.Load() != 0 && m.writeLatencySum.Load() != 0 {
		t.Error("admin latency should not be tracked")
	}
}

func TestRecordError_UnknownType(t *testing.T) {
	m := &Metrics{}
	old := global
	global = m
	defer func() { global = old }()

	// Should not panic for unknown types
	m.RecordError("unknown_type")
	if m.errNoHealthy.Load() != 0 && m.errBadGateway.Load() != 0 {
		t.Error("unknown error type should not increment any counter")
	}
}

func TestSetReplicationStats(t *testing.T) {
	m := &Metrics{}
	old := global
	global = m
	defer func() { global = old }()

	m.SetReplicationStats(42, 7)
	if m.replicationSuccess.Load() != 42 {
		t.Errorf("expected 42 success, got %d", m.replicationSuccess.Load())
	}
	if m.replicationFailure.Load() != 7 {
		t.Errorf("expected 7 failure, got %d", m.replicationFailure.Load())
	}
}

func TestSetNodeRole(t *testing.T) {
	m := &Metrics{}
	old := global
	global = m
	defer func() { global = old }()

	m.SetNodeRole("http://node:7700", "primary")
	v, ok := m.nodeRole.Load("http://node:7700")
	if !ok || v.(string) != "primary" {
		t.Errorf("expected primary, got %v", v)
	}
}

func TestHandler_NodeHealthWithoutRole(t *testing.T) {
	m := &Metrics{}
	old := global
	global = m
	defer func() { global = old }()

	// Set health but not role — should show "unknown" role
	m.SetNodeHealth("http://mystery:7700", true)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	w := httptest.NewRecorder()
	Handler().ServeHTTP(w, req)

	body := w.Body.String()
	if !strings.Contains(body, `role="unknown"`) {
		t.Errorf("expected role=unknown for node without role set\ngot:\n%s", body)
	}
}

func TestConcurrentMetrics(t *testing.T) {
	m := &Metrics{}
	old := global
	global = m
	defer func() { global = old }()

	done := make(chan struct{})
	for i := 0; i < 100; i++ {
		go func() {
			m.RecordRequest("read")
			m.RecordLatency("read", time.Millisecond)
			m.RecordError("bad_gateway")
			m.SetNodeHealth("http://node:7700", true)
			done <- struct{}{}
		}()
	}
	for i := 0; i < 100; i++ {
		<-done
	}

	if m.readRequests.Load() != 100 {
		t.Errorf("expected 100 reads, got %d", m.readRequests.Load())
	}
}
