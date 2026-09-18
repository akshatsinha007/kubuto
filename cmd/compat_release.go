package cmd

import (
	"fmt"

	"github.com/akshatsinha007/kubuto/internal/client"
	"github.com/akshatsinha007/kubuto/internal/config"
	"github.com/akshatsinha007/kubuto/internal/engine"
	"github.com/akshatsinha007/kubuto/pkg/helm"
	"github.com/spf13/cobra"
	"helm.sh/helm/v3/pkg/release"
)

// compatReleaseCmd is `kubuto compat release <name> --target-k8s X`. It
// looks up a Helm release in the live cluster, extracts the chart name and
// chart version from the release manifest, then runs the same flow as
// `compat chart` (compat → optional /recommend follow-up).
//
// Discovery: when the engine returns `not_tracked`, we POST a /discovered
// payload so the engine team can prioritize adding support for charts
// users actually run. The payload field names must match the engine's
// `types.DiscoveredChartRequest` exactly — gin's binding rejects unknown
// shapes.
var (
	compatReleaseCmd = &cobra.Command{
		Use:   "release <release-name>",
		Short: "Check compatibility for a Helm release in your cluster (coming in v2)",
		Long: `Check compatibility for a Helm release deployed in your Kubernetes cluster.
Coming in v2 — not yet usable out of the box.

This command fetches the chart name and version from the Helm release
and checks compatibility with your target Kubernetes version.

Examples:
  # Basic usage (auto-detects chart and version from release)
  kubuto compat release my-nginx --target-k8s 1.28

  # Specify namespace if release is not in default
  kubuto compat release my-app --namespace production --target-k8s 1.27`,
		Args: cobra.ExactArgs(1),
		RunE: runCompatRelease,
	}

	// Flags
	releaseNamespace string
	releaseChartName string // override
	releaseVersion   string // override
	releaseTargetK8s string
	releaseNoCache   bool
)

func init() {
	compatCmd.AddCommand(compatReleaseCmd)

	compatReleaseCmd.Flags().StringVar(&releaseNamespace, "namespace", "", "Kubernetes namespace (default: current context)")
	compatReleaseCmd.Flags().StringVar(&releaseChartName, "chart", "", "Chart name (overrides auto-detect)")
	compatReleaseCmd.Flags().StringVar(&releaseVersion, "version", "", "Chart version (overrides auto-detect)")
	compatReleaseCmd.Flags().StringVar(&releaseTargetK8s, "target-k8s", "", "Target Kubernetes version (required)")
	compatReleaseCmd.Flags().BoolVar(&releaseNoCache, "no-cache", false, "Bypass cache")

	_ = compatReleaseCmd.MarkFlagRequired("target-k8s")
}

func runCompatRelease(cmd *cobra.Command, args []string) error {
	releaseName := args[0]

	ns := releaseNamespace
	if ns == "" {
		var err error
		ns, err = getCurrentNamespace()
		if err != nil {
			ns = "default"
		}
	}

	helmClient, err := helm.NewClient(kubeconfig, "")
	if err != nil {
		return fmt.Errorf("failed to create Helm client: %w", err)
	}

	rel, err := helmClient.GetRelease(cmd.Context(), releaseName, ns)
	if err != nil {
		return fmt.Errorf("failed to get Helm release %s/%s: %w", ns, releaseName, err)
	}

	chart := rel.Chart.Name()
	version := rel.Chart.Metadata.Version

	if releaseChartName != "" {
		chart = releaseChartName
	}
	if releaseVersion != "" {
		version = releaseVersion
	}

	if chart == "" {
		return fmt.Errorf("could not determine chart name from release (use --chart to override)")
	}
	if version == "" {
		return fmt.Errorf("could not determine chart version from release (use --version to override)")
	}

	cfg, err := config.LoadConfig(cfgFile)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	engineClient, err := engine.New(cfg.Engine.URL, cfg.Engine.APIKey, cfg.Engine.Timeout)
	if err != nil {
		return fmt.Errorf("failed to create engine client: %w", err)
	}

	if err := engineClient.HealthCheck(cmd.Context()); err != nil {
		cmd.Printf("⚠️  Warning: Engine health check failed: %v\n\n", err)
	}

	cmd.Printf("Checking compatibility for release %s/%s (chart: %s, version: %s) → K8s %s...\n\n",
		ns, releaseName, chart, version, releaseTargetK8s)

	var resp *engine.CompatResponse
	if releaseNoCache {
		resp, err = engineClient.CheckCompatNoCache(cmd.Context(), chart, version, releaseTargetK8s)
	} else {
		resp, err = engineClient.CheckCompat(cmd.Context(), chart, version, releaseTargetK8s)
	}
	if err != nil {
		// Even on error, fire a /discovered if the engine told us it's
		// untracked (best-effort, non-blocking).
		if resp != nil && resp.Status == "not_tracked" {
			_ = engineClient.LogDiscovered(cmd.Context(), buildDiscoveredRequest(chart, version, rel))
		}
		return fmt.Errorf("engine error: %w", err)
	}

	if resp.Status == "not_tracked" {
		_ = engineClient.LogDiscovered(cmd.Context(), buildDiscoveredRequest(chart, version, rel))
	}

	displayCompatResult(releaseName, ns, chart, version, releaseTargetK8s, resp)

	if shouldShowRecommendation(resp) {
		printRecommendation(cmd, engineClient, chart, releaseTargetK8s)
	}

	return nil
}

// buildDiscoveredRequest extracts the upstream repository URL from the
// Helm release metadata and builds a payload matching the engine's
// `types.DiscoveredChartRequest`. Sources is preferred over Home because
// it tends to point at the chart repository rather than a marketing page.
func buildDiscoveredRequest(chart, version string, rel *helmRelease) engine.DiscoveredRequest {
	repoURL := ""
	if rel != nil && rel.Chart != nil && rel.Chart.Metadata != nil {
		switch {
		case len(rel.Chart.Metadata.Sources) > 0:
			repoURL = rel.Chart.Metadata.Sources[0]
		case rel.Chart.Metadata.Home != "":
			repoURL = rel.Chart.Metadata.Home
		}
	}
	return engine.DiscoveredRequest{
		ChartName:      chart,
		Repository:     repoURL,
		CurrentVersion: version,
		Status:         "discovered",
	}
}

// helmRelease aliases the upstream Helm SDK release type so that the
// signature of buildDiscoveredRequest is short and stable even if we later
// switch helm SDK versions. The local `helm` import is retained because
// helm.NewClient() returns the wrapper used elsewhere in this package.
type helmRelease = release.Release

var _ = helm.NewClient // keep the import alive when we only reference the upstream type above

// getCurrentNamespace resolves the effective namespace from the kubeconfig
// context — the same resolution kubectl itself uses (current context's
// namespace, falling back to "default" only when the context genuinely has
// none set), not a hardcoded "default" regardless of context.
func getCurrentNamespace() (string, error) {
	return client.CurrentNamespace(&config.ClusterConfig{Kubeconfig: kubeconfig})
}
