package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/jward/kagami/internal/service"
)

func newInstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Install systemd service",
		Long:  "Generates a systemd unit file, writes it to /etc/systemd/system/kagami.service, and enables the service.",
		RunE:  runInstall,
	}
}

func runInstall(_ *cobra.Command, _ []string) error {
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("determining executable path: %w", err)
	}

	if err := service.Install(cfgPath, execPath); err != nil {
		return fmt.Errorf("installing service: %w", err)
	}

	fmt.Println("Kagami service installed and enabled.")
	fmt.Println("Start it with: kagami start")
	return nil
}
