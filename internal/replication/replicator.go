package replication

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/a-safe-digital/meilisearch-ha-proxy/internal/health"
)

// Replicator replays write requests from the primary to replica nodes.
type Replicator struct {
	checker *health.Checker
	timeout time.Duration
	client  *http.Client

	mu           sync.RWMutex
	replicaLag   map[string]int64 // url -> last replicated taskUID
	totalSuccess atomic.Int64
	totalFailure atomic.Int64
}

// New creates a Replicator.
func New(checker *health.Checker, timeout time.Duration) *Replicator {
	return &Replicator{
		checker:    checker,
		timeout:    timeout,
		client:     &http.Client{Timeout: timeout},
		replicaLag: make(map[string]int64),
	}
}

// WriteRecord captures the data needed to replicate a write.
type WriteRecord struct {
	Method  string
	Path    string
	Headers http.Header
	Body    []byte
	TaskUID int64
}

// ReplicateAsync sends the write to all replica nodes asynchronously.
// It does not block — replication happens in background goroutines.
func (r *Replicator) ReplicateAsync(rec WriteRecord) {
	replicas := r.checker.HealthyReplicas()
	if len(replicas) == 0 {
		slog.Warn("no healthy replicas for replication", "taskUid", rec.TaskUID)
		return
	}

	for _, node := range replicas {
		go r.replicateToNode(node, rec)
	}
}

func (r *Replicator) replicateToNode(node *health.Node, rec WriteRecord) {
	ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
	defer cancel()

	url := node.URL + rec.Path
	req, err := http.NewRequestWithContext(ctx, rec.Method, url, bytes.NewReader(rec.Body))
	if err != nil {
		slog.Error("replication: create request", "node", node.URL, "error", err)
		r.totalFailure.Add(1)
		return
	}

	// Copy original headers
	for key, values := range rec.Headers {
		for _, v := range values {
			req.Header.Add(key, v)
		}
	}

	// Set the TaskId header for deterministic ordering on replicas
	// (requires --experimental-replication-parameters on the MeiliSearch instance)
	if rec.TaskUID > 0 {
		req.Header.Set("TaskId", strconv.FormatInt(rec.TaskUID, 10))
	}

	// Set auth for the replica
	if node.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+node.APIKey)
	}

	resp, err := r.client.Do(req)
	if err != nil {
		slog.Error("replication: request failed",
			"node", node.URL,
			"taskUid", rec.TaskUID,
			"error", err,
		)
		r.totalFailure.Add(1)
		return
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
		slog.Error("replication: unexpected status",
			"node", node.URL,
			"taskUid", rec.TaskUID,
			"status", resp.StatusCode,
		)
		r.totalFailure.Add(1)
		return
	}

	r.totalSuccess.Add(1)
	r.mu.Lock()
	if rec.TaskUID > r.replicaLag[node.URL] {
		r.replicaLag[node.URL] = rec.TaskUID
	}
	r.mu.Unlock()

	slog.Debug("replication: success",
		"node", node.URL,
		"taskUid", rec.TaskUID,
	)
}

// ReplicaLag returns the last replicated taskUID for each replica.
func (r *Replicator) ReplicaLag() map[string]int64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make(map[string]int64, len(r.replicaLag))
	for k, v := range r.replicaLag {
		result[k] = v
	}
	return result
}

// Stats returns replication success/failure counters.
func (r *Replicator) Stats() (success, failure int64) {
	return r.totalSuccess.Load(), r.totalFailure.Load()
}

// CaptureWrite reads the request body and creates a WriteRecord.
// Returns the body bytes so the caller can still forward the original request.
func CaptureWrite(r *http.Request) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, fmt.Errorf("read request body: %w", err)
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	return body, nil
}
