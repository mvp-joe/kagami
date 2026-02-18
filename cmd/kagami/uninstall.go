package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/jward/kagami/internal/service"
)

func newUninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Uninstall systemd service",
		Long:  "Stops and disables the kagami service, removes the systemd unit file, and runs daemon-reload.",
		RunE:  runUninstall,
	}
}

func runUninstall(_ *cobra.Command, _ []string) error {
	if err := service.Uninstall(); err != nil {
		return fmt.Errorf("uninstalling service: %w", err)
	}

	fmt.Println("Kagami service uninstalled.")
	return nil
}
