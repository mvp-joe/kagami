package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/jward/kagami/internal/config"
)

func newTunnelCmd() *cobra.Command {
	tunnel := &cobra.Command{
		Use:   "tunnel",
		Short: "Manage tunnel entries in config",
	}

	tunnel.AddCommand(
		newTunnelListCmd(),
		newTunnelAddCmd(),
		newTunnelRemoveCmd(),
	)

	return tunnel
}

func newTunnelListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List configured tunnels",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.LoadRaw(cfgPath)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}

			if len(cfg.Tunnel) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No tunnels configured.")
				return nil
			}

			printTunnelTable(cmd.OutOrStdout(), cfg.Tunnel)
			return nil
		},
	}
}

func newTunnelAddCmd() *cobra.Command {
	var (
		name       string
		localAddr  string
		hostname   string
		pathPrefix string
		protocol   string
	)

	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add a tunnel entry to config",
		RunE: func(_ *cobra.Command, _ []string) error {
			if name == "" {
				return fmt.Errorf("--name is required")
			}
			if localAddr == "" {
				return fmt.Errorf("--local-addr is required")
			}
			if hostname == "" && pathPrefix == "" {
				return fmt.Errorf("at least one of --hostname or --path-prefix is required")
			}

			cfg, err := config.LoadRaw(cfgPath)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}

			t := config.TunnelConfig{
				Name:       name,
				LocalAddr:  localAddr,
				Hostname:   hostname,
				PathPrefix: pathPrefix,
				Protocol:   protocol,
			}

			if err := config.AddTunnel(cfg, t); err != nil {
				return err
			}

			if err := config.Save(cfgPath, cfg); err != nil {
				return fmt.Errorf("saving config: %w", err)
			}

			fmt.Printf("Tunnel %q added.\n", name)
			return nil
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "tunnel name (required)")
	cmd.Flags().StringVar(&localAddr, "local-addr", "", "local service address (required)")
	cmd.Flags().StringVar(&hostname, "hostname", "", "public hostname for routing")
	cmd.Flags().StringVar(&pathPrefix, "path-prefix", "", "path prefix for routing")
	cmd.Flags().StringVar(&protocol, "protocol", "", "protocol (http or https, default: http)")

	return cmd
}

func newTunnelRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove [name]",
		Short: "Remove a tunnel entry from config",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			name := args[0]

			cfg, err := config.LoadRaw(cfgPath)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}

			if err := config.RemoveTunnel(cfg, name); err != nil {
				return err
			}

			if err := config.Save(cfgPath, cfg); err != nil {
				return fmt.Errorf("saving config: %w", err)
			}

			fmt.Printf("Tunnel %q removed.\n", name)
			return nil
		},
	}
}
