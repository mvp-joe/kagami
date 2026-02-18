package main

import (
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/jward/kagami/internal/config"
	"github.com/jward/kagami/internal/service"
)

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show agent and service status",
		Long:  "Displays the tunnel ID, server, number of configured tunnels, systemd service status, and a list of configured tunnels.",
		RunE:  runStatus,
	}
}

func runStatus(cmd *cobra.Command, _ []string) error {
	cfg, err := config.LoadRaw(cfgPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	out := cmd.OutOrStdout()

	fmt.Fprintf(out, "Tunnel ID: %s\n", cfg.Agent.TunnelID)
	fmt.Fprintf(out, "Server:    %s\n", cfg.Agent.Server)
	fmt.Fprintf(out, "Tunnels:   %d configured\n", len(cfg.Tunnel))

	// Service status.
	status, err := service.IsActive()
	if err != nil {
		fmt.Fprintf(out, "Service:   unknown (systemctl not available)\n")
	} else {
		fmt.Fprintf(out, "Service:   %s\n", status)
	}

	if len(cfg.Tunnel) > 0 {
		fmt.Fprintln(out)
		printTunnelTable(out, cfg.Tunnel)
	}

	return nil
}

func printTunnelTable(out io.Writer, tunnels []config.TunnelConfig) {
	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tLOCAL ADDR\tHOSTNAME\tPATH PREFIX")
	for _, t := range tunnels {
		hostname := t.Hostname
		if hostname == "" {
			hostname = "-"
		}
		pathPrefix := t.PathPrefix
		if pathPrefix == "" {
			pathPrefix = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", t.Name, t.LocalAddr, hostname, pathPrefix)
	}
	tw.Flush()
}
