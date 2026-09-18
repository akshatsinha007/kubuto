package cmd

import (
	"fmt"
	"strings"
	"time"

	"github.com/akshatsinha007/kubuto/internal/config"
	"github.com/akshatsinha007/kubuto/internal/engine"
	"github.com/spf13/cobra"
)

// compatChartCmd is `kubuto compat chart <name> --version X --target-k8s Y`.
//
// Flow per invocation:
//  1. Resolve engine config (URL + API key) from global config, with the
//     optional `--engine-url` override for CI/dev usage.
//  2. Health-check the engine (best-effort — failure is a warning, not fatal,
//     so users with cached responses still get answers offline).
//  3. Call `/charts/:name/compat`.
//  4. Render the result. If the chart is incompatible OR the engine returned
//     `no_data` for the requested K8s minor, follow up with `/recommend` and
//     surface the recommended chart version inline.
var (
	compatChartCmd = &cobra.Command{
		Use:   "chart <chart-name>",
		Short: "Check compatibility for any Helm chart version (coming in v2)",
		Long: `Check compatibility for any Helm chart version without requiring a cluster.
Coming in v2 — not yet usable out of the box.

Use this for planning upgrades or checking compatibility in CI/CD pipelines.

Examples:
  kubuto compat chart ingress-nginx --version 1.2.3 --target-k8s 1.28
  kubuto compat chart cert-manager --version 1.12.0 --target-k8s 1.27 --no-cache`,
		Args: cobra.ExactArgs(1),
		RunE: runCompatChart,
	}

	// Flags
	chartVersion string
	targetK8s    string
	engineURL    string
	noCache      bool
)

func init() {
	compatCmd.AddCommand(compatChartCmd)

	compatChartCmd.Flags().StringVar(&chartVersion, "version", "", "Chart version (required)")
	compatChartCmd.Flags().StringVar(&targetK8s, "target-k8s", "", "Target Kubernetes version (required)")
	compatChartCmd.Flags().StringVar(&engineURL, "engine-url", "", "Override engine URL from config")
	compatChartCmd.Flags().BoolVar(&noCache, "no-cache", false, "Bypass cache")

	_ = compatChartCmd.MarkFlagRequired("version")
	_ = compatChartCmd.MarkFlagRequired("target-k8s")
}

func runCompatChart(cmd *cobra.Command, args []string) error {
	chartName := args[0]

	if chartVersion == "" {
		return fmt.Errorf("--version is required")
	}
	if targetK8s == "" {
		return fmt.Errorf("--target-k8s is required")
	}

	cfg, err := config.LoadConfig(cfgFile)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	if engineURL != "" {
		cfg.Engine.URL = engineURL
	}

	engineClient, err := engine.New(cfg.Engine.URL, cfg.Engine.APIKey, cfg.Engine.Timeout)
	if err != nil {
		return fmt.Errorf("failed to create engine client: %w", err)
	}

	if err := engineClient.HealthCheck(cmd.Context()); err != nil {
		cmd.Printf("⚠️  Warning: Engine health check failed: %v\n\n", err)
		// Continue anyway — user might be offline but have cached data.
	}

	cmd.Printf("Checking compatibility for %s %s → K8s %s...\n\n",
		chartName, chartVersion, targetK8s)

	var resp *engine.CompatResponse
	if noCache {
		resp, err = engineClient.CheckCompatNoCache(cmd.Context(), chartName, chartVersion, targetK8s)
	} else {
		resp, err = engineClient.CheckCompat(cmd.Context(), chartName, chartVersion, targetK8s)
	}
	if err != nil {
		return fmt.Errorf("engine error: %w", err)
	}

	// Best-effort discovery telemetry, same as compat release — but this
	// command has no Helm release object to pull a repo URL from, so
	// Repository is often empty here. That's fine: chart_name is the only
	// other required field, and an empty repo still tells the engine team
	// this chart is being asked about.
	if resp.Status == "not_tracked" {
		_ = engineClient.LogDiscovered(cmd.Context(), engine.DiscoveredRequest{
			ChartName:      chartName,
			CurrentVersion: chartVersion,
			Status:         "discovered",
		})
	}

	displayCompatResult("", "", chartName, chartVersion, targetK8s, resp)

	// When the user's pinned version doesn't work, fetch and surface the
	// engine's recommendation. This is the headline UX of the CLI: the
	// answer to "what should I install if I upgrade my cluster?".
	if shouldShowRecommendation(resp) {
		printRecommendation(cmd, engineClient, chartName, targetK8s)
	}

	return nil
}

// shouldShowRecommendation returns true when the user would benefit from a
// follow-up `/recommend` call. We deliberately skip it for `not_tracked`
// charts (engine has no data at all) and for `found+compatible` (no upgrade
// needed).
func shouldShowRecommendation(resp *engine.CompatResponse) bool {
	if resp == nil {
		return false
	}
	switch resp.Status {
	case "found":
		return !resp.Compatible
	case "no_data":
		return true
	default:
		return false
	}
}

// printRecommendation calls /recommend and renders a short upgrade hint.
// It never fails the parent command — the recommendation is best-effort UX
// sugar; if it errors we just stay silent.
func printRecommendation(cmd *cobra.Command, c *engine.Client, chart, k8sVer string) {
	rec, err := c.GetRecommendedVersion(cmd.Context(), chart, k8sVer)
	if err != nil {
		return
	}
	switch rec.Status {
	case "found":
		fmt.Printf("\n[RECOMMENDED]     %s %s is compatible with K8s %s — consider upgrading.\n",
			rec.Chart, rec.RecommendedVersion, rec.K8sVersion)
		if rec.IsStale {
			fmt.Printf("                  ⚠️  This recommendation may be stale (last scrape: %s).\n",
				humanizeScrapedAt(rec.ScrapedAt))
		}
		if rec.Source != "" {
			fmt.Printf("                  Source: %s\n", rec.Source)
		}
	case "no_data":
		fmt.Printf("\n[NO_RECOMMENDATION] No chart version is currently known to work on K8s %s.\n", k8sVer)
		fmt.Printf("                    The chart is tracked but the matrix has no compatible row yet.\n")
	}
}

// humanizeScrapedAt converts an RFC3339 timestamp to a human-friendly
// relative form ("12 days ago"). Falls back to the raw string if parsing
// fails — never crashes for malformed input.
func humanizeScrapedAt(rfc3339 string) string {
	if rfc3339 == "" {
		return "unknown"
	}
	t, err := time.Parse(time.RFC3339, rfc3339)
	if err != nil {
		return rfc3339
	}
	delta := time.Since(t)
	switch {
	case delta < 24*time.Hour:
		return fmt.Sprintf("%d hours ago", int(delta.Hours()))
	case delta < 30*24*time.Hour:
		return fmt.Sprintf("%d days ago", int(delta.Hours()/24))
	default:
		return t.Format("2006-01-02")
	}
}

// displayCompatResult renders the primary compat answer. Shared with
// `compat release` so both surfaces look identical.
func displayCompatResult(release, namespace, chart, version, targetK8s string, resp *engine.CompatResponse) {
	fmt.Printf("\n")
	if release != "" {
		fmt.Printf("Release: %s/%s\n", namespace, release)
	}
	fmt.Printf("Chart: %s %s\n", chart, version)
	fmt.Printf("Target: Kubernetes %s\n\n", targetK8s)

	switch resp.Status {
	case "found":
		if resp.Compatible {
			fmt.Printf("[COMPATIBLE]      Works with Kubernetes %s\n", targetK8s)
		} else {
			fmt.Printf("[INCOMPATIBLE]    Does not work with Kubernetes %s\n", targetK8s)
		}
		if resp.IsStale {
			fmt.Printf("[STALE DATA]      ⚠️  Last verified: %s — engine has not re-scraped recently.\n",
				humanizeScrapedAt(resp.ScrapedAt))
		}
		if resp.Source != "" {
			fmt.Printf("Source: %s\n", resp.Source)
		}

	case "no_data":
		fmt.Printf("[NO_DATA]         No compatibility data for %s %s on K8s %s.\n", chart, version, targetK8s)
		if resp.Source != "" {
			fmt.Printf("                  Vendor docs: %s\n", resp.Source)
		}

	case "not_tracked":
		fmt.Printf("[NOT_TRACKED]     This chart is not yet tracked.\n")
		fmt.Printf("                  Help us prioritize: https://kubuto.dev/request-chart\n")

	default:
		// Unknown status — surface what the engine sent so we can debug.
		fmt.Printf("[UNKNOWN]         status=%q message=%q\n", resp.Status, resp.Message)
	}

	if resp.Message != "" && resp.Status != "not_tracked" {
		fmt.Printf("Note: %s\n", strings.TrimSpace(resp.Message))
	}
}
