package cmd

import (
	"fmt"

	"github.com/akshatsinha007/kubuto/internal/config"
	"github.com/akshatsinha007/kubuto/internal/engine"
	"github.com/spf13/cobra"
)

// authValidateCmd implements `kubuto auth validate`, part of `compat`
// (coming in v2). It POSTs the user's configured API key to
// `/api/v1/auth/validate` and surfaces the verdict — plan, rate limit,
// requests already consumed in the current window — so the user can
// quickly diagnose:
//
//   - "Is my key recognized at all?"           → resp.Valid
//   - "Which plan am I on?"                    → resp.Plan
//   - "How close am I to my rate-limit cap?"   → resp.RequestsUsed / resp.RateLimit
//
// The /auth/validate endpoint is public (no auth middleware) so the call
// itself does not consume quota; users can run this safely as often as
// they like.
var authValidateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Validate the configured API key (coming in v2)",
	Long: `Validate that your API key is recognized, and print the plan and
remaining quota for the current window.

Example:
  kubuto auth validate

Configuration is read from the standard kubuto config file
(default: ~/.kubuto/config.yaml). To override a key for a single
invocation, set the KUBUTO_ENGINE_API_KEY environment variable.`,
	RunE: runAuthValidate,
}

func init() {
	authCmd.AddCommand(authValidateCmd)
}

func runAuthValidate(cmd *cobra.Command, _ []string) error {
	cfg, err := config.LoadConfig(cfgFile)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	if cfg.Engine.APIKey == "" {
		return fmt.Errorf("no API key configured (set engine.api_key in your config file or KUBUTO_ENGINE_API_KEY)")
	}

	engineClient, err := engine.New(cfg.Engine.URL, cfg.Engine.APIKey, cfg.Engine.Timeout)
	if err != nil {
		return fmt.Errorf("failed to create engine client: %w", err)
	}

	resp, err := engineClient.ValidateAPIKey(cmd.Context())
	if err != nil {
		return fmt.Errorf("validation request failed: %w", err)
	}

	if !resp.Valid {
		cmd.Println("[INVALID]    Your API key is not recognized.")
		cmd.Println("             Check engine.api_key in your config or generate a new key.")
		// Keep exit code 0 — the call succeeded; the *answer* is "no".
		// CI scripts that need a non-zero on invalid keys can grep stdout.
		return nil
	}

	cmd.Println("[VALID]      Your API key is recognized.")
	if resp.Plan != "" {
		cmd.Printf("Plan:        %s\n", resp.Plan)
	}
	if resp.RateLimit > 0 {
		cmd.Printf("Quota:       %d / %d requests used in current window\n",
			resp.RequestsUsed, resp.RateLimit)
		remaining := resp.RateLimit - resp.RequestsUsed
		if remaining < 0 {
			remaining = 0
		}
		cmd.Printf("Remaining:   %d requests\n", remaining)
	}
	return nil
}
