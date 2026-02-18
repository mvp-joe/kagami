// Kagami agent entry point. Wires up cobra subcommands (run, init,
// project-secret, install, uninstall, start, stop, restart, status,
// tunnel list/add/remove) and delegates to internal packages.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

const defaultConfigPath = "/etc/kagami/kagami.toml"

// cfgPath holds the --config flag value, shared by all subcommands.
var cfgPath string

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "kagami",
		Short: "Kagami tunnel agent",
		Long:  "Kagami mirrors local APIs to the internet through a Cloudflare Durable Object tunnel.",
		// No Run — bare "kagami" prints usage.
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.PersistentFlags().StringVar(&cfgPath, "config", defaultConfigPath, "path to config file")

	root.AddCommand(
		newRunCmd(),
		newProjectSecretCmd(),
		newInitCmd(),
		newInstallCmd(),
		newUninstallCmd(),
		newStartCmd(),
		newStopCmd(),
		newRestartCmd(),
		newStatusCmd(),
		newTunnelCmd(),
	)

	return root
}

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "kagami: %v\n", err)
		os.Exit(1)
	}
}
