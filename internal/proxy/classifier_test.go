package proxy

import (
	"context"
	"net/http"
	"testing"
)

func TestClassify(t *testing.T) {
	tests := []struct {
		name     string
		method   string
		path     string
		expected RequestType
	}{
		// Reads — search operations
		{"POST search", http.MethodPost, "/indexes/movies/search", ReadRequest},
		{"POST multi-search", http.MethodPost, "/multi-search", ReadRequest},
		{"POST facet-search", http.MethodPost, "/indexes/movies/facet-search", ReadRequest},

		// Reads — GET operations
		{"GET documents", http.MethodGet, "/indexes/movies/documents", ReadRequest},
		{"GET single document", http.MethodGet, "/indexes/movies/documents/123", ReadRequest},
		{"GET settings", http.MethodGet, "/indexes/movies/settings", ReadRequest},
		{"GET indexes", http.MethodGet, "/indexes", ReadRequest},
		{"GET single index", http.MethodGet, "/indexes/movies", ReadRequest},

		// Writes — document mutations
		{"POST documents", http.MethodPost, "/indexes/movies/documents", WriteRequest},
		{"PUT documents", http.MethodPut, "/indexes/movies/documents", WriteRequest},
		{"DELETE documents", http.MethodDelete, "/indexes/movies/documents", WriteRequest},
		{"DELETE single document", http.MethodDelete, "/indexes/movies/documents/123", WriteRequest},
		{"POST documents delete-batch", http.MethodPost, "/indexes/movies/documents/delete-batch", WriteRequest},

		// Writes — index mutations
		{"POST create index", http.MethodPost, "/indexes", WriteRequest},
		{"PUT update index", http.MethodPut, "/indexes/movies", WriteRequest},
		{"DELETE index", http.MethodDelete, "/indexes/movies", WriteRequest},

		// Writes — settings
		{"PATCH settings", http.MethodPatch, "/indexes/movies/settings", WriteRequest},
		{"DELETE settings", http.MethodDelete, "/indexes/movies/settings", WriteRequest},

		// Writes — other
		{"POST swap-indexes", http.MethodPost, "/swap-indexes", WriteRequest},
		{"POST dumps", http.MethodPost, "/dumps", WriteRequest},
		{"POST snapshots", http.MethodPost, "/snapshots", WriteRequest},

		// Admin
		{"GET health", http.MethodGet, "/health", AdminRequest},
		{"GET version", http.MethodGet, "/version", AdminRequest},
		{"GET stats", http.MethodGet, "/stats", AdminRequest},
		{"GET tasks", http.MethodGet, "/tasks", AdminRequest},
		{"GET single task", http.MethodGet, "/tasks/123", AdminRequest},
		{"GET keys", http.MethodGet, "/keys", AdminRequest},
		{"GET single key", http.MethodGet, "/keys/abc", AdminRequest},
		{"POST tasks cancel", http.MethodPost, "/tasks/cancel", AdminRequest},
		{"DELETE tasks", http.MethodDelete, "/tasks", AdminRequest},

		// Edge cases
		{"POST search with trailing slash", http.MethodPost, "/indexes/movies/search/", ReadRequest},
		{"GET with trailing slash", http.MethodGet, "/indexes/movies/documents/", ReadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequestWithContext(context.Background(), tt.method, "http://localhost"+tt.path, nil)
			if err != nil {
				t.Fatalf("create request: %v", err)
			}
			got := Classify(req)
			if got != tt.expected {
				t.Errorf("Classify(%s %s) = %s, want %s", tt.method, tt.path, got, tt.expected)
			}
		})
	}
}

func TestRequestTypeString(t *testing.T) {
	tests := []struct {
		rt   RequestType
		want string
	}{
		{ReadRequest, "read"},
		{WriteRequest, "write"},
		{AdminRequest, "admin"},
	}
	for _, tt := range tests {
		if got := tt.rt.String(); got != tt.want {
			t.Errorf("RequestType(%d).String() = %q, want %q", tt.rt, got, tt.want)
		}
	}
}
