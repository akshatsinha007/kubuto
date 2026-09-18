package cmd

import (
	"context"
	"fmt"

	"github.com/akshatsinha007/kubuto/internal/engine"
	"github.com/spf13/cobra"
)

// compatCmd is the parent command for all compatibility-related commands
var compatCmd = &cobra.Command{
	Use:   "compat",
	Short: "Check Kubernetes compatibility for charts and releases (coming in v2)",
	Long: `Compatibility checking commands for Kubernetes Helm charts and deployed releases.

These commands are functional but depend on infrastructure that isn't
publicly available yet — planned for a v2 release. Use "kubuto scan" for
what's usable today.`,
}

// init registers the compat command with the root command
func init() {
	rootCmd.AddCommand(compatCmd)
}

// HealthCheck checks if the engine is reachable
func HealthCheck(ctx context.Context, client *engine.Client) error {
	if err := client.HealthCheck(ctx); err != nil {
		return fmt.Errorf("engine health check failed: %w", err)
	}
	return nil
}
