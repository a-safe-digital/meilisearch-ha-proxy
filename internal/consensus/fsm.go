package consensus

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"sync"

	"github.com/hashicorp/raft"
)

// CommandType identifies the type of FSM command.
type CommandType uint8

const (
	// CmdSetNodeRole changes a node's role (primary/replica).
	CmdSetNodeRole CommandType = iota
	// CmdRecordReplicaLag updates the last replicated taskUID for a node.
	CmdRecordReplicaLag
)

// Command is the payload applied to the FSM via Raft log entries.
type Command struct {
	Type    CommandType `json:"type"`
	NodeURL string      `json:"nodeUrl"`
	Role    string      `json:"role,omitempty"`
	TaskUID int64       `json:"taskUid,omitempty"`
}

// ClusterState represents the replicated cluster state managed by Raft.
type ClusterState struct {
	mu         sync.RWMutex
	NodeRoles  map[string]string `json:"nodeRoles"`  // url -> role
	ReplicaLag map[string]int64  `json:"replicaLag"` // url -> last taskUID
}

// NewClusterState creates an empty cluster state.
func NewClusterState() *ClusterState {
	return &ClusterState{
		NodeRoles:  make(map[string]string),
		ReplicaLag: make(map[string]int64),
	}
}

// GetRole returns the role for a node URL.
func (cs *ClusterState) GetRole(url string) string {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	return cs.NodeRoles[url]
}

// GetLag returns the last replicated taskUID for a node URL.
func (cs *ClusterState) GetLag(url string) int64 {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	return cs.ReplicaLag[url]
}

// FSM implements the raft.FSM interface for cluster state management.
type FSM struct {
	state *ClusterState
}

// NewFSM creates a new FSM with empty state.
func NewFSM() *FSM {
	return &FSM{
		state: NewClusterState(),
	}
}

// State returns the current cluster state.
func (f *FSM) State() *ClusterState {
	return f.state
}

// Apply applies a Raft log entry to the FSM.
func (f *FSM) Apply(log *raft.Log) interface{} {
	var cmd Command
	if err := json.Unmarshal(log.Data, &cmd); err != nil {
		slog.Error("fsm: unmarshal command", "error", err)
		return fmt.Errorf("unmarshal: %w", err)
	}

	switch cmd.Type {
	case CmdSetNodeRole:
		f.state.mu.Lock()
		f.state.NodeRoles[cmd.NodeURL] = cmd.Role
		f.state.mu.Unlock()
		slog.Info("fsm: set node role", "url", cmd.NodeURL, "role", cmd.Role)

	case CmdRecordReplicaLag:
		f.state.mu.Lock()
		if cmd.TaskUID > f.state.ReplicaLag[cmd.NodeURL] {
			f.state.ReplicaLag[cmd.NodeURL] = cmd.TaskUID
		}
		f.state.mu.Unlock()
		slog.Debug("fsm: record replica lag", "url", cmd.NodeURL, "taskUid", cmd.TaskUID)

	default:
		return fmt.Errorf("unknown command type: %d", cmd.Type)
	}

	return nil
}

// Snapshot returns a snapshot of the FSM state.
func (f *FSM) Snapshot() (raft.FSMSnapshot, error) {
	f.state.mu.RLock()
	defer f.state.mu.RUnlock()

	// Deep copy
	roles := make(map[string]string, len(f.state.NodeRoles))
	for k, v := range f.state.NodeRoles {
		roles[k] = v
	}
	lag := make(map[string]int64, len(f.state.ReplicaLag))
	for k, v := range f.state.ReplicaLag {
		lag[k] = v
	}

	return &fsmSnapshot{
		NodeRoles:  roles,
		ReplicaLag: lag,
	}, nil
}

// Restore restores the FSM from a snapshot.
func (f *FSM) Restore(reader io.ReadCloser) error {
	defer reader.Close()

	var snap fsmSnapshot
	if err := json.NewDecoder(reader).Decode(&snap); err != nil {
		return fmt.Errorf("restore: %w", err)
	}

	f.state.mu.Lock()
	f.state.NodeRoles = snap.NodeRoles
	f.state.ReplicaLag = snap.ReplicaLag
	f.state.mu.Unlock()

	slog.Info("fsm: restored from snapshot",
		"nodes", len(snap.NodeRoles),
	)
	return nil
}

// fsmSnapshot implements raft.FSMSnapshot.
type fsmSnapshot struct {
	NodeRoles  map[string]string `json:"nodeRoles"`
	ReplicaLag map[string]int64  `json:"replicaLag"`
}

// Persist serializes the snapshot to the given sink.
func (s *fsmSnapshot) Persist(sink raft.SnapshotSink) error {
	data, err := json.Marshal(s)
	if err != nil {
		sink.Cancel()
		return fmt.Errorf("marshal snapshot: %w", err)
	}

	if _, err := sink.Write(data); err != nil {
		sink.Cancel()
		return fmt.Errorf("write snapshot: %w", err)
	}

	return sink.Close()
}

// Release is a no-op.
func (s *fsmSnapshot) Release() {}
