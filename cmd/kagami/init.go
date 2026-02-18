package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/jward/kagami/internal/config"
)

func newInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Register this machine and write initial config",
		Long:  "Interactively registers this machine with the Kagami Worker, receives a per-machine secret, and writes the initial config file.",
		RunE:  runInit,
	}
}

// registerRequest is the JSON body for POST /_kagami/register.
type registerRequest struct {
	TunnelID string `json:"tunnel_id"`
	Hostname string `json:"hostname,omitempty"`
	OS       string `json:"os,omitempty"`
}

// registerResponse is the JSON response from POST /_kagami/register.
type registerResponse struct {
	MachineID string `json:"machine_id"`
	TunnelID  string `json:"tunnel_id"`
	Secret    string `json:"secret"`
}

func runInit(cmd *cobra.Command, _ []string) error {
	reader := bufio.NewReader(os.Stdin)

	workerURL, err := prompt(reader, "Worker URL (e.g., https://kagami.myworkers.dev): ")
	if err != nil {
		return err
	}
	if workerURL == "" {
		return fmt.Errorf("Worker URL is required")
	}
	u, err := url.Parse(workerURL)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return fmt.Errorf("invalid Worker URL: must start with https:// or http://")
	}
	workerURL = strings.TrimRight(workerURL, "/")

	projectSecret, err := prompt(reader, "Project secret: ")
	if err != nil {
		return err
	}
	if projectSecret == "" {
		return fmt.Errorf("project secret is required")
	}

	machineName, err := prompt(reader, "Machine name (tunnel ID): ")
	if err != nil {
		return err
	}
	if machineName == "" {
		return fmt.Errorf("machine name is required")
	}

	hostname, _ := os.Hostname()
	osName := runtime.GOOS

	// Register with the Worker.
	regReq := registerRequest{
		TunnelID: machineName,
		Hostname: hostname,
		OS:       osName,
	}

	body, err := json.Marshal(regReq)
	if err != nil {
		return fmt.Errorf("marshaling registration request: %w", err)
	}

	req, err := http.NewRequestWithContext(cmd.Context(), http.MethodPost, workerURL+"/_kagami/register", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+projectSecret)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("calling registration API: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading response: %w", err)
	}

	switch resp.StatusCode {
	case http.StatusCreated:
		// success
	case http.StatusUnauthorized:
		return fmt.Errorf("registration failed: invalid project secret (401)")
	case http.StatusConflict:
		return fmt.Errorf("registration failed: tunnel ID %q already exists (409)", machineName)
	default:
		return fmt.Errorf("registration failed: %s — %s", resp.Status, string(respBody))
	}

	var regResp registerResponse
	if err := json.Unmarshal(respBody, &regResp); err != nil {
		return fmt.Errorf("parsing registration response: %w", err)
	}

	// Extract server hostname from the worker URL.
	server := strings.TrimPrefix(workerURL, "https://")
	server = strings.TrimPrefix(server, "http://")

	cfg := &config.Config{
		Agent: config.AgentConfig{
			TunnelID: regResp.TunnelID,
			Secret:   regResp.Secret,
			Server:   server,
			Insecure: u.Scheme == "http",
		},
	}

	// Create config directory if needed.
	configDir := filepath.Dir(cfgPath)
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}

	if err := config.Save(cfgPath, cfg); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}

	fmt.Printf("\nRegistration successful!\n")
	fmt.Printf("  Machine ID: %s\n", regResp.MachineID)
	fmt.Printf("  Tunnel ID:  %s\n", regResp.TunnelID)
	fmt.Printf("  Config:     %s\n", cfgPath)
	fmt.Printf("\nNext steps:\n")
	fmt.Printf("  kagami tunnel add --name api --local-addr localhost:8080 --hostname api.%s.%s\n", regResp.TunnelID, server)
	fmt.Printf("  kagami install\n")
	fmt.Printf("  kagami start\n")
	return nil
}

func prompt(reader *bufio.Reader, message string) (string, error) {
	fmt.Print(message)
	input, err := reader.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("reading input: %w", err)
	}
	return strings.TrimSpace(input), nil
}
