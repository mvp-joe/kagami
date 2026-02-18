package config

import (
	"bytes"
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
)

// Save writes the config to the given path as valid TOML.
// The file is created with mode 0600 (owner-only read/write)
// since it contains the machine secret.
func Save(path string, cfg *Config) error {
	var buf bytes.Buffer
	enc := toml.NewEncoder(&buf)
	if err := enc.Encode(cfg); err != nil {
		return fmt.Errorf("encoding config: %w", err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		return fmt.Errorf("writing config file: %w", err)
	}
	return nil
}

// AddTunnel appends a tunnel entry to the config. Returns an error if
// a tunnel with the same name already exists.
func AddTunnel(cfg *Config, t TunnelConfig) error {
	for _, existing := range cfg.Tunnel {
		if existing.Name == t.Name {
			return fmt.Errorf("tunnel %q already exists", t.Name)
		}
	}
	cfg.Tunnel = append(cfg.Tunnel, t)
	return nil
}

// RemoveTunnel removes the tunnel with the given name from the config.
// Returns an error if no tunnel with that name exists.
func RemoveTunnel(cfg *Config, name string) error {
	for i, t := range cfg.Tunnel {
		if t.Name == name {
			cfg.Tunnel = append(cfg.Tunnel[:i], cfg.Tunnel[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("tunnel %q not found", name)
}

// LoadRaw reads and parses a TOML config file without applying defaults
// or running full validation. This is used by management commands (tunnel
// add/remove) that need to modify and rewrite the config, and may operate
// on configs that have no tunnels yet.
func LoadRaw(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	var cfg Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config file: %w", err)
	}

	return &cfg, nil
}
