package consensus

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRaftNode_LeaderAddr(t *testing.T) {
	dir, err := os.MkdirTemp("", "raft-test-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	cfg := RaftConfig{
		NodeID:    "node-1",
		BindAddr:  "127.0.0.1:0",
		DataDir:   dir,
		Bootstrap: true,
	}

	node, err := NewRaftNode(cfg)
	if err != nil {
		t.Fatalf("create raft node: %v", err)
	}
	defer func() { _ = node.Shutdown() }()

	// Wait for leader
	deadline := time.After(5 * time.Second)
	for !node.IsLeader() {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for leader")
		case <-time.After(50 * time.Millisecond):
		}
	}

	addr := node.LeaderAddr()
	if addr == "" {
		t.Error("expected non-empty leader address")
	}
}

func TestRaftNode_State(t *testing.T) {
	dir, err := os.MkdirTemp("", "raft-test-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	cfg := RaftConfig{
		NodeID:    "node-1",
		BindAddr:  "127.0.0.1:0",
		DataDir:   dir,
		Bootstrap: true,
	}

	node, err := NewRaftNode(cfg)
	if err != nil {
		t.Fatalf("create raft node: %v", err)
	}
	defer func() { _ = node.Shutdown() }()

	// Wait for leader
	deadline := time.After(5 * time.Second)
	for !node.IsLeader() {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for leader")
		case <-time.After(50 * time.Millisecond):
		}
	}

	state := node.State()
	// String() returns "Leader", "Follower", etc.
	if state.String() != "Leader" {
		t.Errorf("expected state 'Leader', got %q", state.String())
	}
}

func TestRaftNode_FSM(t *testing.T) {
	dir, err := os.MkdirTemp("", "raft-test-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	cfg := RaftConfig{
		NodeID:    "node-1",
		BindAddr:  "127.0.0.1:0",
		DataDir:   dir,
		Bootstrap: true,
	}

	node, err := NewRaftNode(cfg)
	if err != nil {
		t.Fatalf("create raft node: %v", err)
	}
	defer func() { _ = node.Shutdown() }()

	if node.FSM() == nil {
		t.Error("expected non-nil FSM")
	}
}

func TestRaftNode_AddVoter(t *testing.T) {
	dir, err := os.MkdirTemp("", "raft-test-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	cfg := RaftConfig{
		NodeID:    "node-1",
		BindAddr:  "127.0.0.1:0",
		DataDir:   dir,
		Bootstrap: true,
	}

	node, err := NewRaftNode(cfg)
	if err != nil {
		t.Fatalf("create raft node: %v", err)
	}
	defer func() { _ = node.Shutdown() }()

	// Wait for leader
	deadline := time.After(5 * time.Second)
	for !node.IsLeader() {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for leader")
		case <-time.After(50 * time.Millisecond):
		}
	}

	// AddVoter for a non-existent peer — should eventually timeout but not panic
	// We use a short timeout to avoid blocking
	err = node.AddVoter("node-2", "127.0.0.1:19999")
	// This may error if the peer is unreachable, but it should not panic
	_ = err
}

func TestFSM_Release(t *testing.T) {
	fsm := NewFSM()
	applyCmd(t, fsm, Command{
		Type:    CmdSetNodeRole,
		NodeURL: "http://meili-0:7700",
		Role:    "primary",
	})

	snap, err := fsm.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	// Release is a no-op — should not panic
	snap.Release()
}

func TestFSM_Restore_InvalidJSON(t *testing.T) {
	fsm := NewFSM()
	err := fsm.Restore(io.NopCloser(bytes.NewReader([]byte("not json"))))
	if err == nil {
		t.Error("expected error restoring invalid JSON")
	}
}

func TestFSM_Persist_WritesValidJSON(t *testing.T) {
	fsm := NewFSM()
	applyCmd(t, fsm, Command{Type: CmdSetNodeRole, NodeURL: "http://a:7700", Role: "primary"})
	applyCmd(t, fsm, Command{Type: CmdRecordReplicaLag, NodeURL: "http://b:7700", TaskUID: 99})

	snap, err := fsm.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	var buf bytes.Buffer
	sink := &mockSink{Writer: &buf}
	if err := snap.Persist(sink); err != nil {
		t.Fatalf("persist: %v", err)
	}

	// Verify it's valid JSON
	var decoded fsmSnapshot
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("persisted data is not valid JSON: %v", err)
	}
	if decoded.NodeRoles["http://a:7700"] != "primary" {
		t.Errorf("expected primary role, got %q", decoded.NodeRoles["http://a:7700"])
	}
	if decoded.ReplicaLag["http://b:7700"] != 99 {
		t.Errorf("expected lag 99, got %d", decoded.ReplicaLag["http://b:7700"])
	}
}

func TestFSM_Persist_CancelOnWriteError(t *testing.T) {
	fsm := NewFSM()
	applyCmd(t, fsm, Command{Type: CmdSetNodeRole, NodeURL: "http://a:7700", Role: "primary"})

	snap, err := fsm.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	sink := &errorSink{}
	err = snap.Persist(sink)
	if err == nil {
		t.Error("expected error when sink.Write fails")
	}
	if !sink.cancelled {
		t.Error("expected sink.Cancel() to be called on write error")
	}
}

func TestClusterState_GetRole_Missing(t *testing.T) {
	cs := NewClusterState()
	role := cs.GetRole("http://nonexistent:7700")
	if role != "" {
		t.Errorf("expected empty role, got %q", role)
	}
}

func TestClusterState_GetLag_Missing(t *testing.T) {
	cs := NewClusterState()
	lag := cs.GetLag("http://nonexistent:7700")
	if lag != 0 {
		t.Errorf("expected 0 lag, got %d", lag)
	}
}

func TestNewRaftNode_WithPeers(t *testing.T) {
	dir, err := os.MkdirTemp("", "raft-test-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	cfg := RaftConfig{
		NodeID:    "node-1",
		BindAddr:  "127.0.0.1:0",
		DataDir:   dir,
		Bootstrap: true,
		Peers:     []string{"127.0.0.1:19001", "127.0.0.1:19002"},
	}

	node, err := NewRaftNode(cfg)
	if err != nil {
		t.Fatalf("create raft node with peers: %v", err)
	}
	defer func() { _ = node.Shutdown() }()
}

func TestNewRaftNode_BadBindAddr(t *testing.T) {
	dir := t.TempDir()
	cfg := RaftConfig{
		NodeID:    "node-1",
		BindAddr:  "999.999.999.999:99999", // invalid address
		DataDir:   dir,
		Bootstrap: false,
	}

	_, err := NewRaftNode(cfg)
	if err == nil {
		t.Error("expected error for invalid bind address")
	}
}

func TestNewRaftNode_ReadOnlyDataDir(t *testing.T) {
	// Create a directory and make it read-only
	dir := t.TempDir()
	readonlyDir := filepath.Join(dir, "readonly")
	if err := os.Mkdir(readonlyDir, 0o555); err != nil {
		t.Fatal(err)
	}
	// Try to create raft store inside a subdir of read-only dir
	cfg := RaftConfig{
		NodeID:    "node-1",
		BindAddr:  "127.0.0.1:0",
		DataDir:   filepath.Join(readonlyDir, "nested", "raft"),
		Bootstrap: false,
	}

	_, err := NewRaftNode(cfg)
	if err == nil {
		t.Error("expected error for read-only data dir")
	}
}

func TestApplyCommand_ResponseError(t *testing.T) {
	dir := t.TempDir()
	cfg := RaftConfig{
		NodeID:    "node-1",
		BindAddr:  "127.0.0.1:0",
		DataDir:   dir,
		Bootstrap: true,
	}

	node, err := NewRaftNode(cfg)
	if err != nil {
		t.Fatalf("create raft node: %v", err)
	}
	defer func() { _ = node.Shutdown() }()

	// Wait for leader
	deadline := time.After(5 * time.Second)
	for !node.IsLeader() {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for leader")
		case <-time.After(50 * time.Millisecond):
		}
	}

	// Apply an unknown command type — FSM returns an error as the response
	err = node.ApplyCommand(Command{
		Type:    CommandType(255),
		NodeURL: "http://test:7700",
	}, 5*time.Second)

	if err == nil {
		t.Error("expected error for unknown command type via Raft")
	}
}

// errorSink is a mock sink that fails on Write.
type errorSink struct {
	cancelled bool
}

func (s *errorSink) ID() string { return "error-test" }
func (s *errorSink) Cancel() error {
	s.cancelled = true
	return nil
}
func (s *errorSink) Close() error { return nil }
func (s *errorSink) Write(_ []byte) (int, error) {
	return 0, io.ErrClosedPipe
}
