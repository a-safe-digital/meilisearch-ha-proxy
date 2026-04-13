package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the root configuration for the HA proxy.
type Config struct {
	Listen        string       `yaml:"listen"`
	MetricsListen string       `yaml:"metrics_listen"`
	LogLevel      string       `yaml:"log_level"`
	Nodes         []NodeConfig `yaml:"nodes"`
	HealthCheck   HealthConfig `yaml:"health_check"`
	Replication   ReplicConfig `yaml:"replication"`
	Raft          RaftConfig   `yaml:"raft"`
}

// RaftConfig controls the Raft consensus layer (optional — disabled if NodeID is empty).
type RaftConfig struct {
	NodeID    string   `yaml:"node_id"`
	BindAddr  string   `yaml:"bind_addr"`
	DataDir   string   `yaml:"data_dir"`
	Peers     []string `yaml:"peers"`
	Bootstrap bool     `yaml:"bootstrap"`
}

// NodeConfig describes a single MeiliSearch backend node.
type NodeConfig struct {
	URL    string `yaml:"url"`
	APIKey string `yaml:"api_key"`
	Role   string `yaml:"role"` // "primary" or "replica"
}

// HealthConfig controls the health checker behaviour.
type HealthConfig struct {
	Interval           time.Duration `yaml:"interval"`
	Timeout            time.Duration `yaml:"timeout"`
	UnhealthyThreshold int           `yaml:"unhealthy_threshold"`
	HealthyThreshold   int           `yaml:"healthy_threshold"`
}

// ReplicConfig controls the write-replication behaviour.
type ReplicConfig struct {
	Timeout        time.Duration `yaml:"timeout"`
	MaxPayloadSize string        `yaml:"max_payload_size"`
	BufferDir      string        `yaml:"buffer_dir"`
}

// Defaults returns a Config populated with sensible defaults.
func Defaults() Config {
	return Config{
		Listen:        ":7700",
		MetricsListen: ":9090",
		LogLevel:      "info",
		HealthCheck: HealthConfig{
			Interval:           5 * time.Second,
			Timeout:            2 * time.Second,
			UnhealthyThreshold: 3,
			HealthyThreshold:   2,
		},
		Replication: ReplicConfig{
			Timeout:        30 * time.Second,
			MaxPayloadSize: "200MB",
			BufferDir:      "/tmp/meili-ha-buffer",
		},
	}
}

// Load reads a YAML config file and applies environment variable overrides.
func Load(path string) (*Config, error) {
	cfg := Defaults()

	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("read config: %w", err)
	}
	if err == nil {
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return nil, fmt.Errorf("parse config: %w", err)
		}
	}

	applyEnvOverrides(&cfg)

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}

	return &cfg, nil
}

// Validate checks that the configuration is valid.
func (c *Config) Validate() error {
	if len(c.Nodes) == 0 {
		return errors.New("at least one node is required")
	}

	primaryCount := 0
	for i, n := range c.Nodes {
		if n.URL == "" {
			return fmt.Errorf("node %d: url is required", i)
		}
		if _, err := url.ParseRequestURI(n.URL); err != nil {
			return fmt.Errorf("node %d: invalid url %q: %w", i, n.URL, err)
		}
		switch n.Role {
		case "primary":
			primaryCount++
		case "replica":
			// ok
		default:
			return fmt.Errorf("node %d: role must be 'primary' or 'replica', got %q", i, n.Role)
		}
	}

	if primaryCount == 0 {
		return errors.New("exactly one node must have role 'primary'")
	}
	if primaryCount > 1 {
		return errors.New("only one node can have role 'primary'")
	}

	if c.HealthCheck.Interval <= 0 {
		return errors.New("health_check.interval must be positive")
	}
	if c.HealthCheck.Timeout <= 0 {
		return errors.New("health_check.timeout must be positive")
	}
	if c.HealthCheck.UnhealthyThreshold <= 0 {
		return errors.New("health_check.unhealthy_threshold must be positive")
	}
	if c.HealthCheck.HealthyThreshold <= 0 {
		return errors.New("health_check.healthy_threshold must be positive")
	}

	return nil
}

func applyEnvOverrides(cfg *Config) {
	if v := os.Getenv("MEILI_HA_LISTEN"); v != "" {
		cfg.Listen = v
	}
	if v := os.Getenv("MEILI_HA_METRICS_LISTEN"); v != "" {
		cfg.MetricsListen = v
	}
	if v := os.Getenv("MEILI_HA_LOG_LEVEL"); v != "" {
		cfg.LogLevel = v
	}
	if v := os.Getenv("MEILI_HA_BUFFER_DIR"); v != "" {
		cfg.Replication.BufferDir = v
	}

	// MEILI_HA_MASTER_KEY overrides api_key on all nodes (used with Kubernetes secretKeyRef)
	if v := os.Getenv("MEILI_HA_MASTER_KEY"); v != "" {
		for i := range cfg.Nodes {
			cfg.Nodes[i].APIKey = v
		}
	}

	// MEILI_HA_NODES format: "url1|key1|role1,url2|key2|role2"
	if v := os.Getenv("MEILI_HA_NODES"); v != "" {
		cfg.Nodes = nil
		for _, entry := range strings.Split(v, ",") {
			parts := strings.SplitN(entry, "|", 3)
			if len(parts) == 3 {
				cfg.Nodes = append(cfg.Nodes, NodeConfig{
					URL:    strings.TrimSpace(parts[0]),
					APIKey: strings.TrimSpace(parts[1]),
					Role:   strings.TrimSpace(parts[2]),
				})
			}
		}
	}
}
