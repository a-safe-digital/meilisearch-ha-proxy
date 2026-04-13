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
  buffer_dir: "/var/lib/meili-ha"
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

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	return path
}
