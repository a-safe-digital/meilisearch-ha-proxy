package proxy

import (
	"encoding/json"
	"net/http"

	"github.com/a-safe-digital/meilisearch-ha-proxy/internal/health"
	"github.com/a-safe-digital/meilisearch-ha-proxy/internal/replication"
)

// AdminHandler handles proxy-specific admin endpoints.
type AdminHandler struct {
	checker    *health.Checker
	replicator *replication.Replicator
	fallback   http.Handler // forwards unhandled admin requests to primary
}

// NewAdminHandler creates an admin handler.
func NewAdminHandler(checker *health.Checker, replicator *replication.Replicator) *AdminHandler {
	return &AdminHandler{
		checker:    checker,
		replicator: replicator,
	}
}

// SetFallback sets a handler for admin requests not handled by the proxy
// (tasks, keys, stats, version — forwarded to primary).
func (a *AdminHandler) SetFallback(h http.Handler) {
	a.fallback = h
}

// ServeHTTP routes admin requests.
func (a *AdminHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := cleanPath(r.URL.Path)

	switch path {
	case "/health":
		a.handleHealth(w, r)
	case "/cluster/status":
		a.handleClusterStatus(w, r)
	default:
		// Forward non-proxy admin requests (tasks, keys, stats, version) to primary
		if a.fallback != nil {
			a.fallback.ServeHTTP(w, r)
			return
		}
		http.Error(w, `{"message":"not found","code":"not_found"}`, http.StatusNotFound)
	}
}

// ClusterHealth represents the aggregated cluster health response.
type ClusterHealth struct {
	Status  string `json:"status"`
	Primary struct {
		URL     string `json:"url"`
		Healthy bool   `json:"healthy"`
	} `json:"primary"`
	HealthyReplicas int `json:"healthyReplicas"`
	TotalReplicas   int `json:"totalReplicas"`
}

func (a *AdminHandler) handleHealth(w http.ResponseWriter, _ *http.Request) {
	primary := a.checker.Primary()
	healthyReplicas := a.checker.HealthyReplicas()
	totalReplicas := 0
	for _, n := range a.checker.Nodes() {
		if n.GetRole() == "replica" {
			totalReplicas++
		}
	}

	resp := ClusterHealth{
		HealthyReplicas: len(healthyReplicas),
		TotalReplicas:   totalReplicas,
	}

	if primary != nil {
		resp.Primary.URL = primary.URL
		resp.Primary.Healthy = primary.IsHealthy()
	}

	// Cluster is healthy if primary is healthy AND at least one replica is healthy
	if primary != nil && primary.IsHealthy() && len(healthyReplicas) > 0 {
		resp.Status = "available"
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
	} else if primary != nil && primary.IsHealthy() {
		resp.Status = "degraded"
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
	} else {
		resp.Status = "unavailable"
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
	}

	_ = json.NewEncoder(w).Encode(resp)
}

// NodeStatus represents a single node in the cluster status response.
type NodeStatus struct {
	URL                string `json:"url"`
	Role               string `json:"role"`
	State              string `json:"state"`
	LastReplicatedTask int64  `json:"lastReplicatedTask,omitempty"`
}

// ClusterStatus represents the full cluster status response.
type ClusterStatus struct {
	Nodes              []NodeStatus `json:"nodes"`
	ReplicationSuccess int64        `json:"replicationSuccess"`
	ReplicationFailure int64        `json:"replicationFailure"`
}

func (a *AdminHandler) handleClusterStatus(w http.ResponseWriter, _ *http.Request) {
	nodes := a.checker.Nodes()
	var lag map[string]int64
	var success, failure int64

	if a.replicator != nil {
		lag = a.replicator.ReplicaLag()
		success, failure = a.replicator.Stats()
	}

	status := ClusterStatus{
		Nodes:              make([]NodeStatus, len(nodes)),
		ReplicationSuccess: success,
		ReplicationFailure: failure,
	}

	for i, n := range nodes {
		status.Nodes[i] = NodeStatus{
			URL:   n.URL,
			Role:  n.GetRole(),
			State: n.State().String(),
		}
		if lag != nil {
			status.Nodes[i].LastReplicatedTask = lag[n.URL]
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(status)
}
