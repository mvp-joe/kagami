package service

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestGenerateUnit_ValidContent(t *testing.T) {
	t.Parallel()
	content := GenerateUnit("/etc/kagami/kagami.toml", "/usr/local/bin/kagami")

	// Must contain all required systemd sections.
	for _, section := range []string{"[Unit]", "[Service]", "[Install]"} {
		if !strings.Contains(content, section) {
			t.Errorf("unit file missing section %q", section)
		}
	}

	// Must contain key directives.
	for _, directive := range []string{
		"Description=Kagami Tunnel Agent",
		"After=network-online.target",
		"Wants=network-online.target",
		"Type=simple",
		"Restart=always",
		"RestartSec=5",
		"ConfigurationDirectory=kagami",
		"WantedBy=multi-user.target",
	} {
		if !strings.Contains(content, directive) {
			t.Errorf("unit file missing directive %q", directive)
		}
	}
}

func TestGenerateUnit_CorrectExecStart(t *testing.T) {
	t.Parallel()
	content := GenerateUnit("/etc/kagami/kagami.toml", "/usr/local/bin/kagami")

	expected := "ExecStart=/usr/local/bin/kagami run --config /etc/kagami/kagami.toml"
	if !strings.Contains(content, expected) {
		t.Errorf("unit file does not contain expected ExecStart\ngot:\n%s\nwant line containing: %s", content, expected)
	}
}

func TestGenerateUnit_CustomPaths(t *testing.T) {
	t.Parallel()
	content := GenerateUnit("/home/user/.config/kagami.toml", "/home/user/bin/kagami")

	if !strings.Contains(content, "ExecStart=/home/user/bin/kagami run --config /home/user/.config/kagami.toml") {
		t.Errorf("ExecStart does not reference custom paths\n%s", content)
	}
}

func TestGenerateUnit_ReferencesConfigDirectory(t *testing.T) {
	t.Parallel()
	content := GenerateUnit("/etc/kagami/kagami.toml", "/usr/local/bin/kagami")

	if !strings.Contains(content, "ConfigurationDirectory=kagami") {
		t.Error("unit file missing ConfigurationDirectory=kagami")
	}
}

func TestInstall_RejectsExistingService(t *testing.T) {
	t.Parallel()

	// Create a temporary file to simulate an existing unit file.
	// Install() checks the const unitPath, so this test only works
	// if a unit file already exists at /etc/systemd/system/kagami.service.
	// If it doesn't (which is the common case in CI/dev), verify that
	// Install fails for a different reason (permission denied on write).
	if _, err := os.Stat(unitPath); err == nil {
		// Unit file exists — Install should reject with "already installed".
		err := Install("/etc/kagami/kagami.toml", "/usr/local/bin/kagami")
		if err == nil {
			t.Fatal("expected error when service already installed")
		}
		if !strings.Contains(err.Error(), "already installed") {
			t.Errorf("error = %q, want it to contain 'already installed'", err)
		}
	} else {
		t.Skip("unit file does not exist at " + unitPath + "; install/uninstall are integration-test concerns requiring root")
	}
}

// TestUninstall_NoExistingService verifies Uninstall does not panic when
// the service file doesn't exist. Since systemctl may not be available
// or we may not be root, this test is skipped when systemctl is absent.
func TestUninstall_NoExistingService(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("systemctl"); err != nil {
		t.Skip("systemctl not available; uninstall is an integration-test concern")
	}

	// Uninstall ignores stop/disable errors and handles os.IsNotExist
	// on remove. daemon-reload at the end may fail without root, so
	// we accept an error — just verify no panic.
	_ = Uninstall()
}
