package proxy

import (
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sync/atomic"

	"github.com/a-safe-digital/meilisearch-ha-proxy/internal/health"
)

// Proxy is the main HTTP handler that routes requests to MeiliSearch backends.
type Proxy struct {
	checker      *health.Checker
	roundRobin   atomic.Uint64
	replicator   Replicator
	adminHandler http.Handler
}

// Replicator handles write replication to follower nodes.
type Replicator interface {
	ReplicateWrite(r *http.Request, body []byte, taskUID int64) error
}

// New creates a Proxy with the given health checker.
func New(checker *health.Checker) *Proxy {
	return &Proxy{
		checker: checker,
	}
}

// SetReplicator sets the write replicator.
func (p *Proxy) SetReplicator(r Replicator) {
	p.replicator = r
}

// SetAdminHandler sets the handler for admin requests.
func (p *Proxy) SetAdminHandler(h http.Handler) {
	p.adminHandler = h
}

// ServeHTTP routes requests based on classification.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	reqType := Classify(r)

	switch reqType {
	case ReadRequest:
		p.handleRead(w, r)
	case WriteRequest:
		p.handleWrite(w, r)
	case AdminRequest:
		p.handleAdmin(w, r)
	}
}

func (p *Proxy) handleRead(w http.ResponseWriter, r *http.Request) {
	nodes := p.checker.HealthyNodes()
	if len(nodes) == 0 {
		http.Error(w, `{"message":"no healthy nodes available","code":"service_unavailable"}`, http.StatusServiceUnavailable)
		return
	}

	// Round-robin selection
	idx := p.roundRobin.Add(1) - 1
	node := nodes[idx%uint64(len(nodes))]

	p.forwardRequest(w, r, node)
}

func (p *Proxy) handleWrite(w http.ResponseWriter, r *http.Request) {
	primary := p.checker.Primary()
	if primary == nil {
		http.Error(w, `{"message":"no primary node configured","code":"internal_error"}`, http.StatusInternalServerError)
		return
	}
	if !primary.IsHealthy() {
		http.Error(w, `{"message":"primary node is unhealthy","code":"service_unavailable"}`, http.StatusServiceUnavailable)
		return
	}

	p.forwardRequest(w, r, primary)
}

func (p *Proxy) handleAdmin(w http.ResponseWriter, r *http.Request) {
	if p.adminHandler != nil {
		p.adminHandler.ServeHTTP(w, r)
		return
	}
	// Default: forward to primary
	primary := p.checker.Primary()
	if primary != nil {
		p.forwardRequest(w, r, primary)
		return
	}
	http.Error(w, `{"message":"no primary node","code":"internal_error"}`, http.StatusInternalServerError)
}

func (p *Proxy) forwardRequest(w http.ResponseWriter, r *http.Request, node *health.Node) {
	targetURL, err := url.Parse(node.URL)
	if err != nil {
		slog.Error("invalid node URL", "url", node.URL, "error", err)
		http.Error(w, `{"message":"internal error","code":"internal_error"}`, http.StatusInternalServerError)
		return
	}

	// Build the outgoing request
	outURL := *r.URL
	outURL.Scheme = targetURL.Scheme
	outURL.Host = targetURL.Host

	outReq, err := http.NewRequestWithContext(r.Context(), r.Method, outURL.String(), r.Body)
	if err != nil {
		slog.Error("create request", "error", err)
		http.Error(w, `{"message":"internal error","code":"internal_error"}`, http.StatusInternalServerError)
		return
	}

	// Copy headers
	for key, values := range r.Header {
		for _, v := range values {
			outReq.Header.Add(key, v)
		}
	}

	// Set auth header for the backend
	if node.APIKey != "" {
		outReq.Header.Set("Authorization", "Bearer "+node.APIKey)
	}

	resp, err := http.DefaultClient.Do(outReq)
	if err != nil {
		slog.Error("forward request", "url", node.URL, "error", err)
		http.Error(w, `{"message":"backend unavailable","code":"service_unavailable"}`, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Copy response headers
	for key, values := range resp.Header {
		for _, v := range values {
			w.Header().Add(key, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}
