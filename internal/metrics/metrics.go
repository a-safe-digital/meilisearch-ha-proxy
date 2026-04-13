package metrics

import (
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// Metrics holds Prometheus-style metrics for the HA proxy.
type Metrics struct {
	// Request counters by type
	readRequests  atomic.Int64
	writeRequests atomic.Int64
	adminRequests atomic.Int64

	// Request latency tracking (histogram-like with sum + count)
	readLatencySum  atomic.Int64 // microseconds
	writeLatencySum atomic.Int64
	readCount       atomic.Int64
	writeCount      atomic.Int64

	// Error counters
	errNoHealthy  atomic.Int64
	errBadGateway atomic.Int64

	// Replication (updated externally)
	replicationSuccess atomic.Int64
	replicationFailure atomic.Int64

	// Node health (set externally)
	nodeHealth sync.Map // url -> int (1=healthy, 0=unhealthy)
	nodeRole   sync.Map // url -> string

	// Failover
	failoverTotal atomic.Int64
}

// Global metrics instance
var global = &Metrics{}

// Get returns the global metrics instance.
func Get() *Metrics { return global }

// RecordRequest increments the request counter for the given type.
func (m *Metrics) RecordRequest(reqType string) {
	switch reqType {
	case "read":
		m.readRequests.Add(1)
	case "write":
		m.writeRequests.Add(1)
	case "admin":
		m.adminRequests.Add(1)
	}
}

// RecordLatency records request latency for the given type.
func (m *Metrics) RecordLatency(reqType string, d time.Duration) {
	us := d.Microseconds()
	switch reqType {
	case "read":
		m.readLatencySum.Add(us)
		m.readCount.Add(1)
	case "write":
		m.writeLatencySum.Add(us)
		m.writeCount.Add(1)
	}
}

// RecordError increments the error counter for the given type.
func (m *Metrics) RecordError(errType string) {
	switch errType {
	case "no_healthy_nodes":
		m.errNoHealthy.Add(1)
	case "bad_gateway":
		m.errBadGateway.Add(1)
	}
}

// SetNodeHealth sets the health status of a node (1=healthy, 0=unhealthy).
func (m *Metrics) SetNodeHealth(url string, healthy bool) {
	if healthy {
		m.nodeHealth.Store(url, 1)
	} else {
		m.nodeHealth.Store(url, 0)
	}
}

// SetNodeRole sets the role of a node.
func (m *Metrics) SetNodeRole(url, role string) {
	m.nodeRole.Store(url, role)
}

// SetReplicationStats updates replication success/failure from the replicator.
func (m *Metrics) SetReplicationStats(success, failure int64) {
	m.replicationSuccess.Store(success)
	m.replicationFailure.Store(failure)
}

// RecordFailover increments the failover counter.
func (m *Metrics) RecordFailover() {
	m.failoverTotal.Add(1)
}

// Handler returns an HTTP handler that serves Prometheus text format metrics.
func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		m := global
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")

		fmt.Fprintf(w, "# HELP meili_ha_requests_total Total proxied requests by type.\n")
		fmt.Fprintf(w, "# TYPE meili_ha_requests_total counter\n")
		fmt.Fprintf(w, "meili_ha_requests_total{type=\"read\"} %d\n", m.readRequests.Load())
		fmt.Fprintf(w, "meili_ha_requests_total{type=\"write\"} %d\n", m.writeRequests.Load())
		fmt.Fprintf(w, "meili_ha_requests_total{type=\"admin\"} %d\n", m.adminRequests.Load())

		fmt.Fprintf(w, "# HELP meili_ha_request_duration_microseconds_sum Sum of request durations.\n")
		fmt.Fprintf(w, "# TYPE meili_ha_request_duration_microseconds_sum counter\n")
		fmt.Fprintf(w, "meili_ha_request_duration_microseconds_sum{type=\"read\"} %d\n", m.readLatencySum.Load())
		fmt.Fprintf(w, "meili_ha_request_duration_microseconds_sum{type=\"write\"} %d\n", m.writeLatencySum.Load())

		fmt.Fprintf(w, "# HELP meili_ha_request_duration_count Number of requests measured.\n")
		fmt.Fprintf(w, "# TYPE meili_ha_request_duration_count counter\n")
		fmt.Fprintf(w, "meili_ha_request_duration_count{type=\"read\"} %d\n", m.readCount.Load())
		fmt.Fprintf(w, "meili_ha_request_duration_count{type=\"write\"} %d\n", m.writeCount.Load())

		fmt.Fprintf(w, "# HELP meili_ha_errors_total Total errors by type.\n")
		fmt.Fprintf(w, "# TYPE meili_ha_errors_total counter\n")
		fmt.Fprintf(w, "meili_ha_errors_total{type=\"no_healthy_nodes\"} %d\n", m.errNoHealthy.Load())
		fmt.Fprintf(w, "meili_ha_errors_total{type=\"bad_gateway\"} %d\n", m.errBadGateway.Load())

		fmt.Fprintf(w, "# HELP meili_ha_replication_total Total replication attempts.\n")
		fmt.Fprintf(w, "# TYPE meili_ha_replication_total counter\n")
		fmt.Fprintf(w, "meili_ha_replication_total{status=\"success\"} %d\n", m.replicationSuccess.Load())
		fmt.Fprintf(w, "meili_ha_replication_total{status=\"failure\"} %d\n", m.replicationFailure.Load())

		fmt.Fprintf(w, "# HELP meili_ha_failover_total Total failover events.\n")
		fmt.Fprintf(w, "# TYPE meili_ha_failover_total counter\n")
		fmt.Fprintf(w, "meili_ha_failover_total %d\n", m.failoverTotal.Load())

		fmt.Fprintf(w, "# HELP meili_ha_node_health Node health status (1=healthy, 0=unhealthy).\n")
		fmt.Fprintf(w, "# TYPE meili_ha_node_health gauge\n")
		m.nodeHealth.Range(func(key, value any) bool {
			url := key.(string)
			h := value.(int)
			role := "unknown"
			if r, ok := m.nodeRole.Load(url); ok {
				role = r.(string)
			}
			fmt.Fprintf(w, "meili_ha_node_health{node=%q,role=%q} %d\n", url, role, h)
			return true
		})
	})
}
