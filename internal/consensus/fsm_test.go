package consensus

import (
	"bytes"
	"encoding/json"
	"io"
	"testing"

	"github.com/hashicorp/raft"
)

func applyCmd(t *testing.T, fsm *FSM, cmd Command) interface{} {
	t.Helper()
	data, err := json.Marshal(cmd)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return fsm.Apply(&raft.Log{Data: data})
}

func TestFSM_SetNodeRole(t *testing.T) {
	fsm := NewFSM()

	result := applyCmd(t, fsm, Command{
		Type:    CmdSetNodeRole,
		NodeURL: "http://meili-0:7700",
		Role:    "primary",
	})

	if result != nil {
		t.Errorf("expected nil result, got %v", result)
	}

	if role := fsm.State().GetRole("http://meili-0:7700"); role != "primary" {
		t.Errorf("expected role 'primary', got %q", role)
	}
}

func TestFSM_SetNodeRoleOverwrite(t *testing.T) {
	fsm := NewFSM()

	applyCmd(t, fsm, Command{
		Type:    CmdSetNodeRole,
		NodeURL: "http://meili-0:7700",
		Role:    "primary",
	})

	applyCmd(t, fsm, Command{
		Type:    CmdSetNodeRole,
		NodeURL: "http://meili-0:7700",
		Role:    "replica",
	})

	if role := fsm.State().GetRole("http://meili-0:7700"); role != "replica" {
		t.Errorf("expected role 'replica', got %q", role)
	}
}

func TestFSM_RecordReplicaLag(t *testing.T) {
	fsm := NewFSM()

	applyCmd(t, fsm, Command{
		Type:    CmdRecordReplicaLag,
		NodeURL: "http://meili-1:7700",
		TaskUID: 42,
	})

	if lag := fsm.State().GetLag("http://meili-1:7700"); lag != 42 {
		t.Errorf("expected lag 42, got %d", lag)
	}
}

func TestFSM_RecordReplicaLag_OnlyIncreases(t *testing.T) {
	fsm := NewFSM()

	applyCmd(t, fsm, Command{
		Type:    CmdRecordReplicaLag,
		NodeURL: "http://meili-1:7700",
		TaskUID: 100,
	})

	// Attempt to set a lower value — should be ignored
	applyCmd(t, fsm, Command{
		Type:    CmdRecordReplicaLag,
		NodeURL: "http://meili-1:7700",
		TaskUID: 50,
	})

	if lag := fsm.State().GetLag("http://meili-1:7700"); lag != 100 {
		t.Errorf("expected lag 100 (not lowered), got %d", lag)
	}
}

func TestFSM_UnknownCommand(t *testing.T) {
	fsm := NewFSM()

	result := applyCmd(t, fsm, Command{
		Type: CommandType(99),
	})

	if result == nil {
		t.Error("expected error for unknown command type")
	}
	if _, ok := result.(error); !ok {
		t.Errorf("expected error type, got %T", result)
	}
}

func TestFSM_InvalidJSON(t *testing.T) {
	fsm := NewFSM()
	result := fsm.Apply(&raft.Log{Data: []byte("not json")})

	if result == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestFSM_SnapshotAndRestore(t *testing.T) {
	// Create FSM with some state
	fsm1 := NewFSM()
	applyCmd(t, fsm1, Command{
		Type:    CmdSetNodeRole,
		NodeURL: "http://meili-0:7700",
		Role:    "primary",
	})
	applyCmd(t, fsm1, Command{
		Type:    CmdSetNodeRole,
		NodeURL: "http://meili-1:7700",
		Role:    "replica",
	})
	applyCmd(t, fsm1, Command{
		Type:    CmdRecordReplicaLag,
		NodeURL: "http://meili-1:7700",
		TaskUID: 55,
	})

	// Take snapshot
	snap, err := fsm1.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	// Persist to buffer
	var buf bytes.Buffer
	sink := &mockSink{Writer: &buf}
	if err := snap.Persist(sink); err != nil {
		t.Fatalf("persist: %v", err)
	}

	// Restore to new FSM
	fsm2 := NewFSM()
	if err := fsm2.Restore(io.NopCloser(&buf)); err != nil {
		t.Fatalf("restore: %v", err)
	}

	// Verify state was restored
	if role := fsm2.State().GetRole("http://meili-0:7700"); role != "primary" {
		t.Errorf("expected primary, got %q", role)
	}
	if role := fsm2.State().GetRole("http://meili-1:7700"); role != "replica" {
		t.Errorf("expected replica, got %q", role)
	}
	if lag := fsm2.State().GetLag("http://meili-1:7700"); lag != 55 {
		t.Errorf("expected lag 55, got %d", lag)
	}
}

func TestFSM_MultipleNodes(t *testing.T) {
	fsm := NewFSM()

	applyCmd(t, fsm, Command{Type: CmdSetNodeRole, NodeURL: "http://a:7700", Role: "primary"})
	applyCmd(t, fsm, Command{Type: CmdSetNodeRole, NodeURL: "http://b:7700", Role: "replica"})
	applyCmd(t, fsm, Command{Type: CmdSetNodeRole, NodeURL: "http://c:7700", Role: "replica"})
	applyCmd(t, fsm, Command{Type: CmdRecordReplicaLag, NodeURL: "http://b:7700", TaskUID: 10})
	applyCmd(t, fsm, Command{Type: CmdRecordReplicaLag, NodeURL: "http://c:7700", TaskUID: 20})

	state := fsm.State()
	if state.GetRole("http://a:7700") != "primary" {
		t.Error("expected a = primary")
	}
	if state.GetRole("http://b:7700") != "replica" {
		t.Error("expected b = replica")
	}
	if state.GetLag("http://b:7700") != 10 {
		t.Error("expected b lag = 10")
	}
	if state.GetLag("http://c:7700") != 20 {
		t.Error("expected c lag = 20")
	}
}

// mockSink implements raft.SnapshotSink for testing.
type mockSink struct {
	io.Writer
	cancelled bool
}

func (s *mockSink) ID() string    { return "test" }
func (s *mockSink) Cancel() error { s.cancelled = true; return nil }
func (s *mockSink) Close() error  { return nil }
