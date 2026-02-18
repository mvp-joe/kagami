package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/BurntSushi/toml"
)

// baseConfig is a minimal agent config used as the starting point
// for tunnel add/remove tests.
const baseConfig = `[agent]
tunnel_id = "my-homelab"
secret = "kgm_mach_abc123"
server = "kagami.myworkers.dev"

[[tunnel]]
name = "api"
local_addr = "localhost:8080"
hostname = "api.my-homelab.kagami.myworkers.dev"
`

func TestAddTunnel_AppendsNew(t *testing.T) {
	t.Parallel()
	path := writeConfig(t, baseConfig)

	cfg, err := LoadRaw(path)
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}

	newTunnel := TunnelConfig{
		Name:      "admin",
		LocalAddr: "localhost:3000",
		Hostname:  "admin.my-homelab.kagami.myworkers.dev",
	}

	if err := AddTunnel(cfg, newTunnel); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := Save(path, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	// Reload and verify.
	reloaded, err := LoadRaw(path)
	if err != nil {
		t.Fatalf("reloading config: %v", err)
	}

	if len(reloaded.Tunnel) != 2 {
		t.Fatalf("got %d tunnels, want 2", len(reloaded.Tunnel))
	}
	if reloaded.Tunnel[1].Name != "admin" {
		t.Errorf("tunnel[1].name = %q, want %q", reloaded.Tunnel[1].Name, "admin")
	}
	if reloaded.Tunnel[1].LocalAddr != "localhost:3000" {
		t.Errorf("tunnel[1].local_addr = %q, want %q", reloaded.Tunnel[1].LocalAddr, "localhost:3000")
	}
}

func TestAddTunnel_DuplicateNameErrors(t *testing.T) {
	t.Parallel()
	cfg, err := LoadRaw(writeConfig(t, baseConfig))
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}

	dup := TunnelConfig{
		Name:      "api",
		LocalAddr: "localhost:9090",
		Hostname:  "api2.example.com",
	}

	err = AddTunnel(cfg, dup)
	if err == nil {
		t.Fatal("expected error for duplicate tunnel name")
	}
}

func TestRemoveTunnel_RemovesExisting(t *testing.T) {
	t.Parallel()
	path := writeConfig(t, baseConfig)

	cfg, err := LoadRaw(path)
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}

	if err := RemoveTunnel(cfg, "api"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := Save(path, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	reloaded, err := LoadRaw(path)
	if err != nil {
		t.Fatalf("reloading config: %v", err)
	}

	if len(reloaded.Tunnel) != 0 {
		t.Fatalf("got %d tunnels, want 0", len(reloaded.Tunnel))
	}
}

func TestRemoveTunnel_NonexistentNameErrors(t *testing.T) {
	t.Parallel()
	cfg, err := LoadRaw(writeConfig(t, baseConfig))
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}

	err = RemoveTunnel(cfg, "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent tunnel name")
	}
}

func TestAddTunnel_ConfigRemainsValidTOML(t *testing.T) {
	t.Parallel()
	path := writeConfig(t, baseConfig)

	cfg, err := LoadRaw(path)
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}

	newTunnel := TunnelConfig{
		Name:       "metrics",
		LocalAddr:  "localhost:9090",
		PathPrefix: "/metrics",
	}

	if err := AddTunnel(cfg, newTunnel); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := Save(path, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	// Verify the written file is valid TOML by parsing it.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading file: %v", err)
	}

	var check Config
	if err := toml.Unmarshal(data, &check); err != nil {
		t.Fatalf("config is not valid TOML after add: %v", err)
	}

	if len(check.Tunnel) != 2 {
		t.Fatalf("got %d tunnels, want 2", len(check.Tunnel))
	}
}

func TestRemoveTunnel_ConfigRemainsValidTOML(t *testing.T) {
	t.Parallel()

	// Start with two tunnels.
	twoTunnels := `[agent]
tunnel_id = "my-homelab"
secret = "kgm_mach_abc123"
server = "kagami.myworkers.dev"

[[tunnel]]
name = "api"
local_addr = "localhost:8080"
hostname = "api.example.com"

[[tunnel]]
name = "admin"
local_addr = "localhost:3000"
hostname = "admin.example.com"
`
	path := writeConfig(t, twoTunnels)

	cfg, err := LoadRaw(path)
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}

	if err := RemoveTunnel(cfg, "api"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := Save(path, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading file: %v", err)
	}

	var check Config
	if err := toml.Unmarshal(data, &check); err != nil {
		t.Fatalf("config is not valid TOML after remove: %v", err)
	}

	if len(check.Tunnel) != 1 {
		t.Fatalf("got %d tunnels, want 1", len(check.Tunnel))
	}
	if check.Tunnel[0].Name != "admin" {
		t.Errorf("remaining tunnel = %q, want %q", check.Tunnel[0].Name, "admin")
	}
}

func TestSave_FilePermissions(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "kagami.toml")

	cfg := &Config{
		Agent: AgentConfig{
			TunnelID: "test",
			Secret:   "kgm_mach_secret",
			Server:   "example.com",
		},
	}

	if err := Save(path, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	perm := info.Mode().Perm()
	if perm != 0o600 {
		t.Errorf("file permissions = %o, want 600", perm)
	}
}

func TestLoadRaw_DoesNotValidateOrApplyDefaults(t *testing.T) {
	t.Parallel()

	// Config with no tunnels — Load() would reject, LoadRaw() should not.
	content := `[agent]
tunnel_id = "my-homelab"
secret = "kgm_mach_abc123"
server = "kagami.myworkers.dev"
`
	cfg, err := LoadRaw(writeConfig(t, content))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(cfg.Tunnel) != 0 {
		t.Errorf("got %d tunnels, want 0", len(cfg.Tunnel))
	}

	// Defaults should NOT be applied.
	if cfg.Agent.PingInterval != "" {
		t.Errorf("ping_interval = %q, want empty (no defaults applied)", cfg.Agent.PingInterval)
	}
}
