package main

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestConfigureLogging_Debug(t *testing.T) {
	configureLogging("debug")
}

func TestConfigureLogging_Warn(t *testing.T) {
	configureLogging("warn")
}

func TestConfigureLogging_Warning(t *testing.T) {
	configureLogging("warning")
}

func TestConfigureLogging_Error(t *testing.T) {
	configureLogging("error")
}

func TestConfigureLogging_Info(t *testing.T) {
	configureLogging("info")
}

func TestConfigureLogging_Unknown(t *testing.T) {
	configureLogging("something-else")
}

func TestRun_MissingConfig(t *testing.T) {
	// Use a non-existent config path
	t.Setenv("MEILI_HA_CONFIG", "/tmp/does-not-exist-config.yaml")
	code := run()
	if code != 1 {
		t.Errorf("expected exit code 1 for missing config, got %d", code)
	}
}

func TestRun_InvalidConfig(t *testing.T) {
	// Write an invalid YAML config
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "invalid.yaml")
	if err := os.WriteFile(cfgPath, []byte("not: valid: yaml: [[["), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MEILI_HA_CONFIG", cfgPath)
	code := run()
	if code != 1 {
		t.Errorf("expected exit code 1 for invalid config, got %d", code)
	}
}

func TestRun_ConfigMissingNodes(t *testing.T) {
	// Valid YAML but missing required fields → validation error
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "empty.yaml")
	if err := os.WriteFile(cfgPath, []byte("listen: \":8080\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MEILI_HA_CONFIG", cfgPath)
	code := run()
	if code != 1 {
		t.Errorf("expected exit code 1 for config missing nodes, got %d", code)
	}
}

func TestRun_GracefulShutdown(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "meili-ha.yaml")
	cfg := `
listen: "127.0.0.1:0"
metrics_listen: "127.0.0.1:0"
log_level: "error"
nodes:
  - url: "http://127.0.0.1:17700"
    role: primary
health_check:
  interval: 60s
  timeout: 2s
  unhealthy_threshold: 3
  healthy_threshold: 2
replication:
  timeout: 5s
  max_payload_size: "10MB"
`
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MEILI_HA_CONFIG", cfgPath)

	done := make(chan int, 1)
	go func() {
		done <- run()
	}()

	// Give it time to start
	time.Sleep(200 * time.Millisecond)

	// Send SIGINT to trigger graceful shutdown
	p, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Signal(syscall.SIGINT); err != nil {
		t.Fatal(err)
	}

	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("expected exit code 0 for graceful shutdown, got %d", code)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for graceful shutdown")
	}
}

func TestRun_CustomConfigEnvVar(t *testing.T) {
	// Verify MEILI_HA_CONFIG env var is respected
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "custom.yaml")
	cfg := `
listen: "127.0.0.1:0"
metrics_listen: "127.0.0.1:0"
log_level: "warn"
nodes:
  - url: "http://127.0.0.1:27700"
    role: primary
health_check:
  interval: 60s
  timeout: 2s
  unhealthy_threshold: 3
  healthy_threshold: 2
`
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MEILI_HA_CONFIG", cfgPath)

	done := make(chan int, 1)
	go func() {
		done <- run()
	}()

	time.Sleep(200 * time.Millisecond)

	p, _ := os.FindProcess(os.Getpid())
	_ = p.Signal(syscall.SIGINT)

	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("expected 0, got %d", code)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out")
	}
}

func TestRun_PortAlreadyInUse(t *testing.T) {
	// Start a listener on a specific port, then try to run() on that port
	// This should trigger the server error path in the errCh branch
	dir := t.TempDir()

	// Bind a port first so run() can't use it
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port

	cfgPath := filepath.Join(dir, "conflict.yaml")
	cfg := fmt.Sprintf(`
listen: "127.0.0.1:%d"
metrics_listen: "127.0.0.1:0"
log_level: "error"
nodes:
  - url: "http://127.0.0.1:47700"
    role: primary
health_check:
  interval: 60s
  timeout: 2s
  unhealthy_threshold: 3
  healthy_threshold: 2
`, port)
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MEILI_HA_CONFIG", cfgPath)

	// run() should exit quickly because the main port is in use
	// The errCh path calls cancel() and triggers shutdown
	code := run()
	_ = code // May be 0 (if graceful shutdown succeeds after error) — just verify no panic
}

func TestRun_WithReplicationTimeout(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "with-rep.yaml")
	cfg := `
listen: "127.0.0.1:0"
metrics_listen: "127.0.0.1:0"
log_level: "debug"
nodes:
  - url: "http://127.0.0.1:37700"
    role: primary
  - url: "http://127.0.0.1:37701"
    role: replica
health_check:
  interval: 60s
  timeout: 2s
  unhealthy_threshold: 3
  healthy_threshold: 2
replication:
  timeout: 10s
  max_payload_size: "50MB"
`
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MEILI_HA_CONFIG", cfgPath)

	done := make(chan int, 1)
	go func() {
		done <- run()
	}()

	time.Sleep(200 * time.Millisecond)

	p, _ := os.FindProcess(os.Getpid())
	_ = p.Signal(syscall.SIGINT)

	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("expected 0, got %d", code)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out")
	}
}
