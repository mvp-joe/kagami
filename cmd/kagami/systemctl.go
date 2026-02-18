package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/jward/kagami/internal/service"
)

func newStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Start the kagami service",
		RunE: func(_ *cobra.Command, _ []string) error {
			if err := service.Start(); err != nil {
				return fmt.Errorf("starting service: %w", err)
			}
			fmt.Println("Kagami service started.")
			return nil
		},
	}
}

func newStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the kagami service",
		RunE: func(_ *cobra.Command, _ []string) error {
			if err := service.Stop(); err != nil {
				return fmt.Errorf("stopping service: %w", err)
			}
			fmt.Println("Kagami service stopped.")
			return nil
		},
	}
}

func newRestartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restart",
		Short: "Restart the kagami service",
		RunE: func(_ *cobra.Command, _ []string) error {
			if err := service.Restart(); err != nil {
				return fmt.Errorf("restarting service: %w", err)
			}
			fmt.Println("Kagami service restarted.")
			return nil
		},
	}
}
