package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"github.com/spf13/cobra"
)

func newProjectSecretCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "project-secret",
		Short: "Generate a random project secret",
		Long:  "Generates a cryptographically random project secret for use with the Kagami Cloudflare Worker.",
		RunE:  runProjectSecret,
	}
}

func runProjectSecret(_ *cobra.Command, _ []string) error {
	secret, err := generateProjectSecret()
	if err != nil {
		return err
	}

	fmt.Printf("Generated project secret:\n  %s\n\n", secret)
	fmt.Println("Set this in your Cloudflare Worker:")
	fmt.Println("  wrangler secret put KAGAMI_PROJECT_SECRET")
	return nil
}

// generateProjectSecret creates a kgm_proj_ prefixed secret with 32 bytes
// of cryptographic randomness encoded as hex (64 chars).
func generateProjectSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating random bytes: %w", err)
	}
	return "kgm_proj_" + hex.EncodeToString(b), nil
}
