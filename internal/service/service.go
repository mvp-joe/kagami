// Package service handles systemd unit file generation and lifecycle
// management. Generates /etc/systemd/system/kagami.service, and wraps
// systemctl enable/disable/start/stop/restart/status commands.
package service

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const unitPath = "/etc/systemd/system/kagami.service"

// unitTemplate is the systemd unit file template.
// %s placeholders: ExecStart path, config path.
const unitTemplate = `[Unit]
Description=Kagami Tunnel Agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=%s run --config %s
Restart=always
RestartSec=5
ConfigurationDirectory=kagami

[Install]
WantedBy=multi-user.target
`

// GenerateUnit returns the content of a systemd unit file for kagami.
func GenerateUnit(configPath, execPath string) string {
	return fmt.Sprintf(unitTemplate, execPath, configPath)
}

// Install writes the systemd unit file to /etc/systemd/system/kagami.service,
// runs daemon-reload, and enables the service.
func Install(configPath, execPath string) error {
	if _, err := os.Stat(unitPath); err == nil {
		return fmt.Errorf("service already installed at %s (use kagami uninstall first to remove)", unitPath)
	}

	content := GenerateUnit(configPath, execPath)

	if err := os.WriteFile(unitPath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("writing unit file: %w", err)
	}

	if err := systemctl("daemon-reload"); err != nil {
		return fmt.Errorf("daemon-reload: %w", err)
	}
	if err := systemctl("enable", "kagami"); err != nil {
		return fmt.Errorf("enabling service: %w", err)
	}

	return nil
}

// Uninstall stops and disables the service, removes the unit file, and
// runs daemon-reload. Errors from stop/disable are ignored (service may
// not be running or enabled).
func Uninstall() error {
	_ = systemctl("stop", "kagami")
	_ = systemctl("disable", "kagami")

	if err := os.Remove(unitPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing unit file: %w", err)
	}

	if err := systemctl("daemon-reload"); err != nil {
		return fmt.Errorf("daemon-reload: %w", err)
	}

	return nil
}

// Start starts the kagami service via systemctl.
func Start() error {
	return systemctl("start", "kagami")
}

// Stop stops the kagami service via systemctl.
func Stop() error {
	return systemctl("stop", "kagami")
}

// Restart restarts the kagami service via systemctl.
func Restart() error {
	return systemctl("restart", "kagami")
}

// IsActive returns the output of systemctl is-active kagami.
// The returned string is one of: "active", "inactive", "failed", etc.
// An error is returned only if the command itself fails to execute (not
// for non-zero exit codes, which are normal for inactive services).
func IsActive() (string, error) {
	out, err := exec.Command("systemctl", "is-active", "kagami").CombinedOutput()
	result := strings.TrimSpace(string(out))
	if err != nil {
		// systemctl is-active returns non-zero for inactive/failed, which
		// is expected, not an error. Only report if we can't run at all.
		if result == "" {
			return "", fmt.Errorf("checking service status: %w", err)
		}
	}
	return result, nil
}

// systemctl runs a systemctl command with the given arguments.
func systemctl(args ...string) error {
	cmd := exec.Command("systemctl", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
