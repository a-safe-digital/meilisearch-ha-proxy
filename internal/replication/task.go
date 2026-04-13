package replication

import (
	"encoding/json"
	"fmt"
)

// TaskResponse represents the MeiliSearch 202 response for async operations.
type TaskResponse struct {
	TaskUID    int64  `json:"taskUid"`
	IndexUID   string `json:"indexUid"`
	Status     string `json:"status"`
	Type       string `json:"type"`
	EnqueuedAt string `json:"enqueuedAt"`
}

// ExtractTaskUID parses a MeiliSearch 202 response body and extracts the taskUid.
func ExtractTaskUID(body []byte) (int64, error) {
	var resp TaskResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return 0, fmt.Errorf("parse task response: %w", err)
	}
	if resp.TaskUID == 0 && resp.Status == "" {
		return 0, fmt.Errorf("no taskUid in response")
	}
	return resp.TaskUID, nil
}
