package consensus

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/hashicorp/raft"
	raftboltdb "github.com/hashicorp/raft-boltdb/v2"
)

// RaftConfig holds configuration for the Raft consensus layer.
type RaftConfig struct {
	NodeID    string   // unique ID for this proxy node
	BindAddr  string   // address for Raft transport (e.g., "0.0.0.0:7701")
	DataDir   string   // directory for Raft log and snapshots
	Peers     []string // addresses of other proxy nodes
	Bootstrap bool     // true if this node should bootstrap the cluster
}

// RaftNode wraps a hashicorp/raft instance with helper methods.
type RaftNode struct {
	raft *raft.Raft
	fsm  *FSM
}

// NewRaftNode creates and starts a Raft node.
func NewRaftNode(cfg RaftConfig) (*RaftNode, error) {
	fsm := NewFSM()

	raftCfg := raft.DefaultConfig()
	raftCfg.LocalID = raft.ServerID(cfg.NodeID)
	raftCfg.SnapshotThreshold = 1024
	raftCfg.SnapshotInterval = 30 * time.Second

	// Silence Raft's internal logger (we use slog)
	raftCfg.LogLevel = "WARN"

	// Set up transport
	addr, err := net.ResolveTCPAddr("tcp", cfg.BindAddr)
	if err != nil {
		return nil, fmt.Errorf("resolve bind addr: %w", err)
	}

	transport, err := raft.NewTCPTransport(cfg.BindAddr, addr, 3, 10*time.Second, os.Stderr)
	if err != nil {
		return nil, fmt.Errorf("create transport: %w", err)
	}

	// Set up store
	if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}

	boltStore, err := raftboltdb.NewBoltStore(filepath.Join(cfg.DataDir, "raft.db"))
	if err != nil {
		return nil, fmt.Errorf("create bolt store: %w", err)
	}

	snapshotStore, err := raft.NewFileSnapshotStore(cfg.DataDir, 2, os.Stderr)
	if err != nil {
		return nil, fmt.Errorf("create snapshot store: %w", err)
	}

	r, err := raft.NewRaft(raftCfg, fsm, boltStore, boltStore, snapshotStore, transport)
	if err != nil {
		return nil, fmt.Errorf("create raft: %w", err)
	}

	// Bootstrap if this is the initial leader
	if cfg.Bootstrap {
		servers := []raft.Server{
			{
				ID:      raft.ServerID(cfg.NodeID),
				Address: raft.ServerAddress(cfg.BindAddr),
			},
		}
		// Add peers
		for _, peer := range cfg.Peers {
			servers = append(servers, raft.Server{
				ID:      raft.ServerID(peer),
				Address: raft.ServerAddress(peer),
			})
		}

		future := r.BootstrapCluster(raft.Configuration{
			Servers: servers,
		})
		if err := future.Error(); err != nil && err != raft.ErrCantBootstrap {
			return nil, fmt.Errorf("bootstrap: %w", err)
		}
	}

	slog.Info("raft node started",
		"id", cfg.NodeID,
		"bind", cfg.BindAddr,
		"bootstrap", cfg.Bootstrap,
	)

	return &RaftNode{
		raft: r,
		fsm:  fsm,
	}, nil
}

// IsLeader returns true if this node is the Raft leader.
func (rn *RaftNode) IsLeader() bool {
	return rn.raft.State() == raft.Leader
}

// LeaderAddr returns the address of the current Raft leader.
func (rn *RaftNode) LeaderAddr() string {
	addr, _ := rn.raft.LeaderWithID()
	return string(addr)
}

// State returns the current Raft state (Leader, Follower, Candidate).
func (rn *RaftNode) State() raft.RaftState {
	return rn.raft.State()
}

// FSM returns the finite state machine.
func (rn *RaftNode) FSM() *FSM {
	return rn.fsm
}

// ApplyCommand applies a command through the Raft log.
// Only the leader can apply commands.
func (rn *RaftNode) ApplyCommand(cmd Command, timeout time.Duration) error {
	data, err := json.Marshal(cmd)
	if err != nil {
		return fmt.Errorf("marshal command: %w", err)
	}

	future := rn.raft.Apply(data, timeout)
	if err := future.Error(); err != nil {
		return fmt.Errorf("apply: %w", err)
	}

	if resp := future.Response(); resp != nil {
		if err, ok := resp.(error); ok {
			return err
		}
	}

	return nil
}

// AddVoter adds a new voting node to the cluster.
func (rn *RaftNode) AddVoter(id, addr string) error {
	future := rn.raft.AddVoter(raft.ServerID(id), raft.ServerAddress(addr), 0, 10*time.Second)
	return future.Error()
}

// Shutdown gracefully shuts down the Raft node.
func (rn *RaftNode) Shutdown() error {
	return rn.raft.Shutdown().Error()
}
