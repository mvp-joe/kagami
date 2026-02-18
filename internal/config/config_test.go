package config

import (
	"os"
	"path/filepath"
	"testing"
)

// writeConfig writes content to a temp TOML file and returns the path.
func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "kagami.toml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing temp config: %v", err)
	}
	return path
}

const validOneTunnel = `
[agent]
tunnel_id = "my-homelab"
secret = "kgm_mach_abc123"
server = "kagami.myworkers.dev"

[[tunnel]]
name = "api"
local_addr = "localhost:8080"
hostname = "api.my-homelab.kagami.myworkers.dev"
`

const validMultiTunnel = `
[agent]
tunnel_id = "my-homelab"
secret = "kgm_mach_abc123"
server = "kagami.myworkers.dev"

[[tunnel]]
name = "api"
local_addr = "localhost:8080"
hostname = "api.my-homelab.kagami.myworkers.dev"

[[tunnel]]
name = "admin"
local_addr = "localhost:3000"
hostname = "admin.my-homelab.kagami.myworkers.dev"
path_prefix = "/admin"
protocol = "https"
`

func TestLoad_ValidOneTunnel(t *testing.T) {
	t.Parallel()
	cfg, err := Load(writeConfig(t, validOneTunnel))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Agent.TunnelID != "my-homelab" {
		t.Errorf("tunnel_id = %q, want %q", cfg.Agent.TunnelID, "my-homelab")
	}
	if cfg.Agent.Secret != "kgm_mach_abc123" {
		t.Errorf("secret = %q, want %q", cfg.Agent.Secret, "kgm_mach_abc123")
	}
	if cfg.Agent.Server != "kagami.myworkers.dev" {
		t.Errorf("server = %q, want %q", cfg.Agent.Server, "kagami.myworkers.dev")
	}

	if len(cfg.Tunnel) != 1 {
		t.Fatalf("got %d tunnels, want 1", len(cfg.Tunnel))
	}
	tun := cfg.Tunnel[0]
	if tun.Name != "api" {
		t.Errorf("tunnel name = %q, want %q", tun.Name, "api")
	}
	if tun.LocalAddr != "localhost:8080" {
		t.Errorf("local_addr = %q, want %q", tun.LocalAddr, "localhost:8080")
	}
	if tun.Hostname != "api.my-homelab.kagami.myworkers.dev" {
		t.Errorf("hostname = %q, want %q", tun.Hostname, "api.my-homelab.kagami.myworkers.dev")
	}
}

func TestLoad_ValidMultipleTunnels(t *testing.T) {
	t.Parallel()
	cfg, err := Load(writeConfig(t, validMultiTunnel))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(cfg.Tunnel) != 2 {
		t.Fatalf("got %d tunnels, want 2", len(cfg.Tunnel))
	}

	if cfg.Tunnel[0].Name != "api" {
		t.Errorf("tunnel[0].name = %q, want %q", cfg.Tunnel[0].Name, "api")
	}
	if cfg.Tunnel[1].Name != "admin" {
		t.Errorf("tunnel[1].name = %q, want %q", cfg.Tunnel[1].Name, "admin")
	}
	if cfg.Tunnel[1].LocalAddr != "localhost:3000" {
		t.Errorf("tunnel[1].local_addr = %q, want %q", cfg.Tunnel[1].LocalAddr, "localhost:3000")
	}
	if cfg.Tunnel[1].PathPrefix != "/admin" {
		t.Errorf("tunnel[1].path_prefix = %q, want %q", cfg.Tunnel[1].PathPrefix, "/admin")
	}
	if cfg.Tunnel[1].Protocol != "https" {
		t.Errorf("tunnel[1].protocol = %q, want %q", cfg.Tunnel[1].Protocol, "https")
	}
}

func TestLoad_MissingAgentSection(t *testing.T) {
	t.Parallel()
	content := `
[[tunnel]]
name = "api"
local_addr = "localhost:8080"
hostname = "api.example.com"
`
	_, err := Load(writeConfig(t, content))
	if err == nil {
		t.Fatal("expected error for missing [agent] section")
	}
}

func TestLoad_MissingTunnelID(t *testing.T) {
	t.Parallel()
	content := `
[agent]
secret = "kgm_mach_abc123"
server = "kagami.myworkers.dev"

[[tunnel]]
name = "api"
local_addr = "localhost:8080"
hostname = "api.example.com"
`
	_, err := Load(writeConfig(t, content))
	if err == nil {
		t.Fatal("expected error for missing tunnel_id")
	}
	if got := err.Error(); got != "config: agent.tunnel_id is required" {
		t.Errorf("error = %q, want %q", got, "config: agent.tunnel_id is required")
	}
}

func TestLoad_MissingSecret(t *testing.T) {
	t.Parallel()
	content := `
[agent]
tunnel_id = "my-homelab"
server = "kagami.myworkers.dev"

[[tunnel]]
name = "api"
local_addr = "localhost:8080"
hostname = "api.example.com"
`
	_, err := Load(writeConfig(t, content))
	if err == nil {
		t.Fatal("expected error for missing secret")
	}
	if got := err.Error(); got != "config: agent.secret is required" {
		t.Errorf("error = %q, want %q", got, "config: agent.secret is required")
	}
}

func TestLoad_MissingServer(t *testing.T) {
	t.Parallel()
	content := `
[agent]
tunnel_id = "my-homelab"
secret = "kgm_mach_abc123"

[[tunnel]]
name = "api"
local_addr = "localhost:8080"
hostname = "api.example.com"
`
	_, err := Load(writeConfig(t, content))
	if err == nil {
		t.Fatal("expected error for missing server")
	}
	if got := err.Error(); got != "config: agent.server is required" {
		t.Errorf("error = %q, want %q", got, "config: agent.server is required")
	}
}

func TestLoad_EmptyTunnelArray(t *testing.T) {
	t.Parallel()
	content := `
[agent]
tunnel_id = "my-homelab"
secret = "kgm_mach_abc123"
server = "kagami.myworkers.dev"
`
	_, err := Load(writeConfig(t, content))
	if err == nil {
		t.Fatal("expected error for empty tunnel array")
	}
	if got := err.Error(); got != "config: at least one [[tunnel]] entry is required" {
		t.Errorf("error = %q, want %q", got, "config: at least one [[tunnel]] entry is required")
	}
}

func TestLoad_TunnelMissingLocalAddr(t *testing.T) {
	t.Parallel()
	content := `
[agent]
tunnel_id = "my-homelab"
secret = "kgm_mach_abc123"
server = "kagami.myworkers.dev"

[[tunnel]]
name = "api"
hostname = "api.example.com"
`
	_, err := Load(writeConfig(t, content))
	if err == nil {
		t.Fatal("expected error for missing local_addr")
	}
}

func TestLoad_TunnelMissingHostnameAndPathPrefix(t *testing.T) {
	t.Parallel()
	content := `
[agent]
tunnel_id = "my-homelab"
secret = "kgm_mach_abc123"
server = "kagami.myworkers.dev"

[[tunnel]]
name = "api"
local_addr = "localhost:8080"
`
	_, err := Load(writeConfig(t, content))
	if err == nil {
		t.Fatal("expected error for missing hostname and path_prefix")
	}
}

func TestLoad_DefaultValues(t *testing.T) {
	t.Parallel()
	cfg, err := Load(writeConfig(t, validOneTunnel))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Agent duration defaults.
	if cfg.Agent.PingInterval != DefaultPingInterval {
		t.Errorf("ping_interval = %q, want %q", cfg.Agent.PingInterval, DefaultPingInterval)
	}
	if cfg.Agent.ReconnectInterval != DefaultReconnectInterval {
		t.Errorf("reconnect_interval = %q, want %q", cfg.Agent.ReconnectInterval, DefaultReconnectInterval)
	}
	if cfg.Agent.MaxReconnectInterval != DefaultMaxReconnectInterval {
		t.Errorf("max_reconnect_interval = %q, want %q", cfg.Agent.MaxReconnectInterval, DefaultMaxReconnectInterval)
	}
	if cfg.Agent.ProxyTimeout != DefaultProxyTimeout {
		t.Errorf("proxy_timeout = %q, want %q", cfg.Agent.ProxyTimeout, DefaultProxyTimeout)
	}

	// Tunnel protocol default.
	if cfg.Tunnel[0].Protocol != DefaultProtocol {
		t.Errorf("tunnel[0].protocol = %q, want %q", cfg.Tunnel[0].Protocol, DefaultProtocol)
	}
}

func TestLoad_InvalidTOMLSyntax(t *testing.T) {
	t.Parallel()
	content := `
[agent]
tunnel_id = "my-homelab"
secret = kgm_mach_abc123  # missing quotes
server = "kagami.myworkers.dev"
`
	_, err := Load(writeConfig(t, content))
	if err == nil {
		t.Fatal("expected error for invalid TOML syntax")
	}
	// BurntSushi/toml includes line info in parse errors.
	t.Logf("parse error: %v", err)
}

func TestLoad_InvalidDuration(t *testing.T) {
	t.Parallel()
	content := `
[agent]
tunnel_id = "my-homelab"
secret = "kgm_mach_abc123"
server = "kagami.myworkers.dev"
ping_interval = "not-a-duration"

[[tunnel]]
name = "api"
local_addr = "localhost:8080"
hostname = "api.example.com"
`
	_, err := Load(writeConfig(t, content))
	if err == nil {
		t.Fatal("expected error for invalid duration")
	}
}

func TestLoad_FileNotFound(t *testing.T) {
	t.Parallel()
	_, err := Load("/nonexistent/path/kagami.toml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoad_TunnelWithPathPrefixOnly(t *testing.T) {
	t.Parallel()
	content := `
[agent]
tunnel_id = "my-homelab"
secret = "kgm_mach_abc123"
server = "kagami.myworkers.dev"

[[tunnel]]
name = "api"
local_addr = "localhost:8080"
path_prefix = "/api"
`
	cfg, err := Load(writeConfig(t, content))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Tunnel[0].PathPrefix != "/api" {
		t.Errorf("path_prefix = %q, want %q", cfg.Tunnel[0].PathPrefix, "/api")
	}
}

func TestLoad_CustomDurations(t *testing.T) {
	t.Parallel()
	content := `
[agent]
tunnel_id = "my-homelab"
secret = "kgm_mach_abc123"
server = "kagami.myworkers.dev"
ping_interval = "1m"
reconnect_interval = "10s"
max_reconnect_interval = "2m"
proxy_timeout = "15s"

[[tunnel]]
name = "api"
local_addr = "localhost:8080"
hostname = "api.example.com"
`
	cfg, err := Load(writeConfig(t, content))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Agent.PingInterval != "1m" {
		t.Errorf("ping_interval = %q, want %q", cfg.Agent.PingInterval, "1m")
	}
	if cfg.Agent.ReconnectInterval != "10s" {
		t.Errorf("reconnect_interval = %q, want %q", cfg.Agent.ReconnectInterval, "10s")
	}
	if cfg.Agent.MaxReconnectInterval != "2m" {
		t.Errorf("max_reconnect_interval = %q, want %q", cfg.Agent.MaxReconnectInterval, "2m")
	}
	if cfg.Agent.ProxyTimeout != "15s" {
		t.Errorf("proxy_timeout = %q, want %q", cfg.Agent.ProxyTimeout, "15s")
	}
}
