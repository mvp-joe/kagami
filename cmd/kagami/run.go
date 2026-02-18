package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/jward/kagami/internal/config"
	"github.com/jward/kagami/internal/proxy"
	"github.com/jward/kagami/internal/tunnel"
)

func newRunCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "run",
		Short: "Start the tunnel agent (foreground)",
		Long:  "Runs the kagami tunnel agent in the foreground. Connects to the Cloudflare Worker via WebSocket and proxies incoming requests to configured local services.",
		RunE:  runRun,
	}
}

func runRun(cmd *cobra.Command, _ []string) error {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	logger.Info("loading config", "path", cfgPath)

	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	proxyTimeout, err := time.ParseDuration(cfg.Agent.ProxyTimeout)
	if err != nil {
		return fmt.Errorf("parsing proxy_timeout: %w", err)
	}

	router := proxy.NewRouter(cfg.Tunnel)
	p := proxy.NewProxy(proxyTimeout)

	client, err := tunnel.NewClient(cfg, router, p, logger)
	if err != nil {
		return fmt.Errorf("creating tunnel client: %w", err)
	}

	ctx, cancel := context.WithCancel(cmd.Context())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(sigCh)

	go func() {
		sig := <-sigCh
		logger.Info("received signal, shutting down", "signal", sig)
		cancel()
	}()

	logger.Info("starting kagami agent",
		"tunnel_id", cfg.Agent.TunnelID,
		"server", cfg.Agent.Server,
		"tunnels", len(cfg.Tunnel),
	)

	if err := client.Run(ctx); err != nil {
		return fmt.Errorf("tunnel client: %w", err)
	}

	logger.Info("draining in-flight requests")
	client.Shutdown(10 * time.Second)

	logger.Info("kagami agent stopped")
	return nil
}
