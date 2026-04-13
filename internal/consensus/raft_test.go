package consensus

import (
	"os"
	"testing"
	"time"
)

func TestRaftNode_SingleNodeCluster(t *testing.T) {
	dir, err := os.MkdirTemp("", "raft-test-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	cfg := RaftConfig{
		NodeID:    "node-1",
		BindAddr:  "127.0.0.1:0", // random port
		DataDir:   dir,
		Bootstrap: true,
	}

	node, err := NewRaftNode(cfg)
	if err != nil {
		t.Fatalf("create raft node: %v", err)
	}
	defer node.Shutdown()

	// Wait for leader election (single node bootstraps as leader)
	deadline := time.After(5 * time.Second)
	for !node.IsLeader() {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for leader election")
		case <-time.After(50 * time.Millisecond):
		}
	}

	if !node.IsLeader() {
		t.Error("expected single node to be leader")
	}
}

func TestRaftNode_ApplyCommand(t *testing.T) {
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
	defer node.Shutdown()

	// Wait for leader
	deadline := time.After(5 * time.Second)
	for !node.IsLeader() {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for leader election")
		case <-time.After(50 * time.Millisecond):
		}
	}

	// Apply a command
	err = node.ApplyCommand(Command{
		Type:    CmdSetNodeRole,
		NodeURL: "http://meili-0:7700",
		Role:    "primary",
	}, 5*time.Second)

	if err != nil {
		t.Fatalf("apply command: %v", err)
	}

	// Verify FSM state
	role := node.FSM().State().GetRole("http://meili-0:7700")
	if role != "primary" {
		t.Errorf("expected role 'primary', got %q", role)
	}
}

func TestRaftNode_MultipleCommands(t *testing.T) {
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
	defer node.Shutdown()

	// Wait for leader
	deadline := time.After(5 * time.Second)
	for !node.IsLeader() {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for leader election")
		case <-time.After(50 * time.Millisecond):
		}
	}

	// Apply multiple commands
	commands := []Command{
		{Type: CmdSetNodeRole, NodeURL: "http://meili-0:7700", Role: "primary"},
		{Type: CmdSetNodeRole, NodeURL: "http://meili-1:7700", Role: "replica"},
		{Type: CmdRecordReplicaLag, NodeURL: "http://meili-1:7700", TaskUID: 42},
	}

	for _, cmd := range commands {
		if err := node.ApplyCommand(cmd, 5*time.Second); err != nil {
			t.Fatalf("apply command: %v", err)
		}
	}

	state := node.FSM().State()
	if state.GetRole("http://meili-0:7700") != "primary" {
		t.Error("expected meili-0 = primary")
	}
	if state.GetRole("http://meili-1:7700") != "replica" {
		t.Error("expected meili-1 = replica")
	}
	if state.GetLag("http://meili-1:7700") != 42 {
		t.Errorf("expected lag 42, got %d", state.GetLag("http://meili-1:7700"))
	}
}

func TestRaftNode_NonLeaderCannotApply(t *testing.T) {
	dir, err := os.MkdirTemp("", "raft-test-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	cfg := RaftConfig{
		NodeID:    "node-1",
		BindAddr:  "127.0.0.1:0",
		DataDir:   dir,
		Bootstrap: false, // not bootstrapped — will not become leader
	}

	node, err := NewRaftNode(cfg)
	if err != nil {
		t.Fatalf("create raft node: %v", err)
	}
	defer node.Shutdown()

	// Give it a moment, but it should NOT become leader
	time.Sleep(500 * time.Millisecond)

	if node.IsLeader() {
		t.Skip("node unexpectedly became leader")
	}

	// Try to apply — should fail
	err = node.ApplyCommand(Command{
		Type:    CmdSetNodeRole,
		NodeURL: "http://meili-0:7700",
		Role:    "primary",
	}, 1*time.Second)

	if err == nil {
		t.Error("expected error when non-leader tries to apply")
	}
}
