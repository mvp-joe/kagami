package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jward/kagami/internal/config"
)

const testBaseConfig = `[agent]
tunnel_id = "my-homelab"
secret = "kgm_mach_abc123"
server = "kagami.myworkers.dev"

[[tunnel]]
name = "api"
local_addr = "localhost:8080"
hostname = "api.my-homelab.kagami.myworkers.dev"
`

func setupConfigFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "kagami.toml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing config: %v", err)
	}
	return path
}

// executeCommand runs a cobra command with the given args and captures stdout.
func executeCommand(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(args)
	err := root.Execute()
	return buf.String(), err
}

func TestTunnelAdd_ViaCommand(t *testing.T) {
	path := setupConfigFile(t, testBaseConfig)

	_, err := executeCommand(t,
		"--config", path,
		"tunnel", "add",
		"--name", "admin",
		"--local-addr", "localhost:3000",
		"--hostname", "admin.my-homelab.kagami.myworkers.dev",
	)
	if err != nil {
		t.Fatalf("tunnel add failed: %v", err)
	}

	cfg, err := config.LoadRaw(path)
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}

	if len(cfg.Tunnel) != 2 {
		t.Fatalf("got %d tunnels, want 2", len(cfg.Tunnel))
	}
	if cfg.Tunnel[1].Name != "admin" {
		t.Errorf("tunnel[1].name = %q, want %q", cfg.Tunnel[1].Name, "admin")
	}
}

func TestTunnelAdd_DuplicateName(t *testing.T) {
	path := setupConfigFile(t, testBaseConfig)

	_, err := executeCommand(t,
		"--config", path,
		"tunnel", "add",
		"--name", "api",
		"--local-addr", "localhost:9090",
		"--hostname", "api2.example.com",
	)
	if err == nil {
		t.Fatal("expected error for duplicate tunnel name")
	}
}

func TestTunnelAdd_MissingName(t *testing.T) {
	path := setupConfigFile(t, testBaseConfig)

	_, err := executeCommand(t,
		"--config", path,
		"tunnel", "add",
		"--local-addr", "localhost:3000",
		"--hostname", "admin.example.com",
	)
	if err == nil {
		t.Fatal("expected error for missing --name")
	}
}

func TestTunnelAdd_MissingLocalAddr(t *testing.T) {
	path := setupConfigFile(t, testBaseConfig)

	_, err := executeCommand(t,
		"--config", path,
		"tunnel", "add",
		"--name", "admin",
		"--hostname", "admin.example.com",
	)
	if err == nil {
		t.Fatal("expected error for missing --local-addr")
	}
}

func TestTunnelAdd_MissingHostnameAndPathPrefix(t *testing.T) {
	path := setupConfigFile(t, testBaseConfig)

	_, err := executeCommand(t,
		"--config", path,
		"tunnel", "add",
		"--name", "admin",
		"--local-addr", "localhost:3000",
	)
	if err == nil {
		t.Fatal("expected error for missing hostname and path-prefix")
	}
}

func TestTunnelRemove_ViaCommand(t *testing.T) {
	path := setupConfigFile(t, testBaseConfig)

	_, err := executeCommand(t,
		"--config", path,
		"tunnel", "remove", "api",
	)
	if err != nil {
		t.Fatalf("tunnel remove failed: %v", err)
	}

	cfg, err := config.LoadRaw(path)
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}

	if len(cfg.Tunnel) != 0 {
		t.Fatalf("got %d tunnels, want 0", len(cfg.Tunnel))
	}
}

func TestTunnelRemove_NonexistentName(t *testing.T) {
	path := setupConfigFile(t, testBaseConfig)

	_, err := executeCommand(t,
		"--config", path,
		"tunnel", "remove", "nonexistent",
	)
	if err == nil {
		t.Fatal("expected error for nonexistent tunnel name")
	}
}

func TestTunnelList_DisplaysTunnels(t *testing.T) {
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
path_prefix = "/admin"
`
	path := setupConfigFile(t, twoTunnels)

	// Verify tunnel list doesn't error and the config is read correctly.
	_, err := executeCommand(t,
		"--config", path,
		"tunnel", "list",
	)
	if err != nil {
		t.Fatalf("tunnel list failed: %v", err)
	}

	// Verify the config was readable and has expected tunnels.
	cfg, err := config.LoadRaw(path)
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}
	if len(cfg.Tunnel) != 2 {
		t.Fatalf("got %d tunnels, want 2", len(cfg.Tunnel))
	}
}

func TestTunnelList_Empty(t *testing.T) {
	noTunnels := `[agent]
tunnel_id = "my-homelab"
secret = "kgm_mach_abc123"
server = "kagami.myworkers.dev"
`
	path := setupConfigFile(t, noTunnels)

	_, err := executeCommand(t,
		"--config", path,
		"tunnel", "list",
	)
	if err != nil {
		t.Fatalf("tunnel list failed: %v", err)
	}
}

func TestTunnelAdd_WithProtocol(t *testing.T) {
	path := setupConfigFile(t, testBaseConfig)

	_, err := executeCommand(t,
		"--config", path,
		"tunnel", "add",
		"--name", "secure",
		"--local-addr", "localhost:443",
		"--hostname", "secure.example.com",
		"--protocol", "https",
	)
	if err != nil {
		t.Fatalf("tunnel add failed: %v", err)
	}

	cfg, err := config.LoadRaw(path)
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}

	if cfg.Tunnel[1].Protocol != "https" {
		t.Errorf("protocol = %q, want %q", cfg.Tunnel[1].Protocol, "https")
	}
}

func TestTunnelAdd_WithPathPrefixOnly(t *testing.T) {
	path := setupConfigFile(t, testBaseConfig)

	_, err := executeCommand(t,
		"--config", path,
		"tunnel", "add",
		"--name", "metrics",
		"--local-addr", "localhost:9090",
		"--path-prefix", "/metrics",
	)
	if err != nil {
		t.Fatalf("tunnel add failed: %v", err)
	}

	cfg, err := config.LoadRaw(path)
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}

	if cfg.Tunnel[1].PathPrefix != "/metrics" {
		t.Errorf("path_prefix = %q, want %q", cfg.Tunnel[1].PathPrefix, "/metrics")
	}
	if cfg.Tunnel[1].Hostname != "" {
		t.Errorf("hostname = %q, want empty", cfg.Tunnel[1].Hostname)
	}
}

// TestProjectSecret_GeneratesSecret verifies the project-secret command runs
// and produces output containing the kgm_proj_ prefix.
func TestProjectSecret_Output(t *testing.T) {
	secret, err := generateProjectSecret()
	if err != nil {
		t.Fatalf("generating secret: %v", err)
	}

	if !strings.HasPrefix(secret, "kgm_proj_") {
		t.Errorf("secret = %q, want prefix %q", secret, "kgm_proj_")
	}

	// 9 chars prefix + 64 chars hex = 73 total
	if len(secret) != 73 {
		t.Errorf("secret length = %d, want 73", len(secret))
	}
}

// TestProjectSecret_Uniqueness verifies two generated secrets are different.
func TestProjectSecret_Uniqueness(t *testing.T) {
	s1, err := generateProjectSecret()
	if err != nil {
		t.Fatalf("generating secret 1: %v", err)
	}
	s2, err := generateProjectSecret()
	if err != nil {
		t.Fatalf("generating secret 2: %v", err)
	}
	if s1 == s2 {
		t.Error("two generated secrets are identical")
	}
}

// Ensure the list output captures tunnel names via cobra's SetOut buffer.
func TestTunnelList_OutputContainsTunnelNames(t *testing.T) {
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
	path := setupConfigFile(t, twoTunnels)

	output, err := executeCommand(t,
		"--config", path,
		"tunnel", "list",
	)
	if err != nil {
		t.Fatalf("tunnel list failed: %v", err)
	}

	if !strings.Contains(output, "api") {
		t.Errorf("output missing tunnel name 'api'\n%s", output)
	}
	if !strings.Contains(output, "admin") {
		t.Errorf("output missing tunnel name 'admin'\n%s", output)
	}
	if !strings.Contains(output, "localhost:8080") {
		t.Errorf("output missing local addr 'localhost:8080'\n%s", output)
	}
}
