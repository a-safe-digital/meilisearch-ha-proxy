package proxy

import (
	"net/http"
	"strings"
)

// RequestType classifies a request for routing.
type RequestType int

const (
	// ReadRequest is a search or document read — route to any healthy node.
	ReadRequest RequestType = iota
	// WriteRequest is a document/index/settings mutation — forward to primary.
	WriteRequest
	// AdminRequest is a health/version/stats check — handle locally or forward.
	AdminRequest
)

func (rt RequestType) String() string {
	switch rt {
	case ReadRequest:
		return "read"
	case WriteRequest:
		return "write"
	case AdminRequest:
		return "admin"
	default:
		return "unknown"
	}
}

// Classify determines the request type based on HTTP method and path.
//
// Classification rules:
//   - All GET requests are reads (except admin endpoints)
//   - POST /indexes/{uid}/search, /multi-search, /indexes/{uid}/facet-search are reads
//   - Admin: /health, /version, /stats, /keys (GET)
//   - Everything else (POST/PUT/PATCH/DELETE on indexes/documents/settings) is a write
func Classify(r *http.Request) RequestType {
	path := cleanPath(r.URL.Path)
	method := r.Method

	// Admin endpoints
	if isAdminPath(path) {
		return AdminRequest
	}

	// Read-type POST endpoints (search operations)
	if method == http.MethodPost {
		if path == "/multi-search" {
			return ReadRequest
		}
		if strings.HasSuffix(path, "/search") && strings.HasPrefix(path, "/indexes/") {
			return ReadRequest
		}
		if strings.HasSuffix(path, "/facet-search") && strings.HasPrefix(path, "/indexes/") {
			return ReadRequest
		}
		return WriteRequest
	}

	// All other mutating methods are writes
	if method == http.MethodPut || method == http.MethodPatch || method == http.MethodDelete {
		return WriteRequest
	}

	// GET requests are reads
	return ReadRequest
}

func isAdminPath(path string) bool {
	switch {
	case path == "/health":
		return true
	case path == "/version":
		return true
	case path == "/stats":
		return true
	case path == "/keys" || strings.HasPrefix(path, "/keys/"):
		return true
	case path == "/tasks" || strings.HasPrefix(path, "/tasks/"):
		return true
	case path == "/cluster/status":
		return true
	default:
		return false
	}
}

func cleanPath(path string) string {
	// Remove trailing slash
	if len(path) > 1 && path[len(path)-1] == '/' {
		path = path[:len(path)-1]
	}
	return path
}
