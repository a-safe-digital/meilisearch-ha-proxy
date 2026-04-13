package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaults(t *testing.T) {
	cfg := Defaults()
	if cfg.Listen != ":7700" {
		t.Errorf("expected listen :7700, got %s", cfg.Listen)
	}
	if cfg.MetricsListen != ":9090" {
		t.Errorf("expected metrics_listen :9090, got %s", cfg.MetricsListen)
	}
	if cfg.LogLevel != "info" {
		t.Errorf("expected log_level info, got %s", cfg.LogLevel)
	}
	if cfg.Replication.MaxPayloadSize != "200MB" {
		t.Errorf("expected max_payload_size 200MB, got %s", cfg.Replication.MaxPayloadSize)
	}
}

func TestLoadValidConfig(t *testing.T) {
	yaml := `
listen: ":8080"
metrics_listen: ":9091"
log_level: "debug"
nodes:
  - url: "http://meili-0:7700"
    api_key: "key0"
    role: primary
  - url: "http://meili-1:7700"
    api_key: "key1"
    role: replica
health_check:
  interval: 10s
  timeout: 3s
  unhealthy_threshold: 5
  healthy_threshold: 3
replication:
  timeout: 60s
  max_payload_size: "500MB"
`
	path := writeTemp(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Listen != ":8080" {
		t.Errorf("expected :8080, got %s", cfg.Listen)
	}
	if len(cfg.Nodes) != 2 {
		t.Errorf("expected 2 nodes, got %d", len(cfg.Nodes))
	}
	if cfg.Nodes[0].Role != "primary" {
		t.Errorf("expected primary, got %s", cfg.Nodes[0].Role)
	}
	if cfg.Nodes[1].Role != "replica" {
		t.Errorf("expected replica, got %s", cfg.Nodes[1].Role)
	}
	if cfg.HealthCheck.UnhealthyThreshold != 5 {
		t.Errorf("expected unhealthy_threshold 5, got %d", cfg.HealthCheck.UnhealthyThreshold)
	}
	if cfg.Replication.MaxPayloadSize != "500MB" {
		t.Errorf("expected max_payload_size 500MB, got %s", cfg.Replication.MaxPayloadSize)
	}
}

func TestLoadMissingNodes(t *testing.T) {
	yaml := `
listen: ":7700"
nodes: []
`
	path := writeTemp(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for empty nodes")
	}
}

func TestLoadNoPrimary(t *testing.T) {
	yaml := `
nodes:
  - url: "http://meili-0:7700"
    role: replica
`
	path := writeTemp(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for no primary")
	}
}

func TestLoadMultiplePrimaries(t *testing.T) {
	yaml := `
nodes:
  - url: "http://meili-0:7700"
    role: primary
  - url: "http://meili-1:7700"
    role: primary
`
	path := writeTemp(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for multiple primaries")
	}
}

func TestLoadInvalidRole(t *testing.T) {
	yaml := `
nodes:
  - url: "http://meili-0:7700"
    role: leader
`
	path := writeTemp(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid role")
	}
}

func TestLoadInvalidURL(t *testing.T) {
	yaml := `
nodes:
  - url: "://bad"
    role: primary
`
	path := writeTemp(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid URL")
	}
}

func TestLoadMissingURL(t *testing.T) {
	yaml := `
nodes:
  - role: primary
`
	path := writeTemp(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for missing URL")
	}
}

func TestLoadInvalidYAML(t *testing.T) {
	path := writeTemp(t, `{{{invalid yaml`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestValidateHealthCheckInterval(t *testing.T) {
	cfg := Defaults()
	cfg.Nodes = []NodeConfig{{URL: "http://meili:7700", Role: "primary"}}
	cfg.HealthCheck.Interval = 0
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for zero interval")
	}
}

func TestValidateHealthCheckTimeout(t *testing.T) {
	cfg := Defaults()
	cfg.Nodes = []NodeConfig{{URL: "http://meili:7700", Role: "primary"}}
	cfg.HealthCheck.Timeout = -1
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for negative timeout")
	}
}

func TestValidateUnhealthyThreshold(t *testing.T) {
	cfg := Defaults()
	cfg.Nodes = []NodeConfig{{URL: "http://meili:7700", Role: "primary"}}
	cfg.HealthCheck.UnhealthyThreshold = 0
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for zero unhealthy threshold")
	}
}

func TestValidateHealthyThreshold(t *testing.T) {
	cfg := Defaults()
	cfg.Nodes = []NodeConfig{{URL: "http://meili:7700", Role: "primary"}}
	cfg.HealthCheck.HealthyThreshold = 0
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for zero healthy threshold")
	}
}

func TestEnvOverrides(t *testing.T) {
	yaml := `
listen: ":7700"
nodes:
  - url: "http://meili-0:7700"
    role: primary
`
	path := writeTemp(t, yaml)

	t.Setenv("MEILI_HA_LISTEN", ":9999")
	t.Setenv("MEILI_HA_LOG_LEVEL", "warn")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Listen != ":9999" {
		t.Errorf("expected :9999, got %s", cfg.Listen)
	}
	if cfg.LogLevel != "warn" {
		t.Errorf("expected warn, got %s", cfg.LogLevel)
	}
}

func TestEnvOverrideMetricsListen(t *testing.T) {
	yaml := `
nodes:
  - url: "http://meili-0:7700"
    role: primary
`
	path := writeTemp(t, yaml)
	t.Setenv("MEILI_HA_METRICS_LISTEN", ":1234")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.MetricsListen != ":1234" {
		t.Errorf("expected :1234, got %s", cfg.MetricsListen)
	}
}

func TestEnvOverrideMasterKey(t *testing.T) {
	yaml := `
nodes:
  - url: "http://meili-0:7700"
    api_key: "old-key"
    role: primary
  - url: "http://meili-1:7700"
    api_key: "old-key"
    role: replica
`
	path := writeTemp(t, yaml)
	t.Setenv("MEILI_HA_MASTER_KEY", "new-master-key")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for i, n := range cfg.Nodes {
		if n.APIKey != "new-master-key" {
			t.Errorf("node %d: expected api_key 'new-master-key', got %q", i, n.APIKey)
		}
	}
}

func TestEnvOverrideNodes(t *testing.T) {
	yaml := `
listen: ":7700"
nodes:
  - url: "http://old:7700"
    role: primary
`
	path := writeTemp(t, yaml)

	t.Setenv("MEILI_HA_NODES", "http://new-0:7700|key0|primary,http://new-1:7700|key1|replica")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(cfg.Nodes))
	}
	if cfg.Nodes[0].URL != "http://new-0:7700" {
		t.Errorf("expected http://new-0:7700, got %s", cfg.Nodes[0].URL)
	}
	if cfg.Nodes[1].APIKey != "key1" {
		t.Errorf("expected key1, got %s", cfg.Nodes[1].APIKey)
	}
}

func TestEnvOverrideNodesMalformed(t *testing.T) {
	yaml := `
listen: ":7700"
nodes:
  - url: "http://old:7700"
    role: primary
`
	path := writeTemp(t, yaml)

	// Malformed entries (only 2 parts instead of 3) should be silently dropped
	t.Setenv("MEILI_HA_NODES", "http://a:7700|key|primary,bad-entry|only-two")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Nodes) != 1 {
		t.Errorf("expected 1 valid node (malformed dropped), got %d", len(cfg.Nodes))
	}
}

func TestLoadNonexistentFileUsesDefaults(t *testing.T) {
	// With env-provided nodes, a missing config file should work using defaults
	t.Setenv("MEILI_HA_NODES", "http://meili-0:7700|key0|primary")

	cfg, err := Load("/tmp/does-not-exist-meili-ha.yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Listen != ":7700" {
		t.Errorf("expected default listen :7700, got %s", cfg.Listen)
	}
}

// ── ParseSize ─────────────────────────────────────────────────────

func TestParseSize(t *testing.T) {
	tests := []struct {
		input    string
		expected int64
	}{
		{"", 0},
		{"  ", 0},
		{"0B", 0},
		{"100B", 100},
		{"1KB", 1024},
		{"10kb", 10 * 1024},
		{"200MB", 200 * 1024 * 1024},
		{"1GB", 1024 * 1024 * 1024},
		{"  200MB  ", 200 * 1024 * 1024},
		{"invalid", 0},
		{"MB", 0},
		{"abc123", 0},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := ParseSize(tt.input)
			if got != tt.expected {
				t.Errorf("ParseSize(%q) = %d, want %d", tt.input, got, tt.expected)
			}
		})
	}
}

func TestLoad_ReadError(t *testing.T) {
	// Passing a directory as the config path should cause a read error
	dir := t.TempDir()
	_, err := Load(dir) // dir is not a file
	if err == nil {
		t.Error("expected error when loading a directory as config")
	}
}

func TestLoad_NonExistentFile_UsesDefaults(t *testing.T) {
	// Non-existent file should use defaults + env override
	t.Setenv("MEILI_HA_NODES", "http://primary:7700|key|primary")
	cfg, err := Load("/tmp/this-file-does-not-exist-12345.yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Listen != ":7700" {
		t.Errorf("expected default listen :7700, got %s", cfg.Listen)
	}
	if len(cfg.Nodes) != 1 {
		t.Fatalf("expected 1 node from env, got %d", len(cfg.Nodes))
	}
	if cfg.Nodes[0].URL != "http://primary:7700" {
		t.Errorf("expected primary URL, got %s", cfg.Nodes[0].URL)
	}
}

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	return path
}
