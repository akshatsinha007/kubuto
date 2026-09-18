package cmd

import (
	"fmt"

	"github.com/akshatsinha007/kubuto/internal/client"
	"github.com/akshatsinha007/kubuto/internal/config"
	"github.com/akshatsinha007/kubuto/internal/engine"
	"github.com/akshatsinha007/kubuto/internal/output"
	"github.com/akshatsinha007/kubuto/internal/scanner"
	"github.com/akshatsinha007/kubuto/internal/utils"
	"github.com/akshatsinha007/kubuto/pkg/types"
	"github.com/spf13/cobra"
)

var compatClusterCmd = &cobra.Command{
	Use:   "cluster",
	Short: "Check all deployed charts against a target Kubernetes version (coming in v2)",
	Long: `Scan the cluster for deployed Helm charts and check each one's compatibility
with a target Kubernetes version. This is the "pre-upgrade" command — it tells
you which charts will break when you upgrade K8s. Coming in v2 — not yet
usable out of the box.

If --target-k8s-version is not provided, the current cluster's K8s version will be
auto-detected and used for compatibility checks.

Examples:
  # Auto-detect current cluster K8s version
  kubuto compat cluster

  # Check compatibility with a target version
  kubuto compat cluster --target-k8s-version 1.31

  # Include ArgoCD-deployed charts
  kubuto compat cluster --target-k8s-version 1.31 --gitops argocd

  # Include both ArgoCD- and Flux-deployed charts alongside plain Helm
  # releases, all in one compatibility check
  kubuto compat cluster --target-k8s-version 1.31 --gitops all

  # Export results to JSON
  kubuto compat cluster --target-k8s-version 1.32 --export results.json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		k8sVersion, _ := cmd.Flags().GetString("target-k8s-version")

		cfg, err := config.LoadConfig(cfgFile)
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		if url, _ := cmd.Flags().GetString("engine-url"); url != "" {
			cfg.Engine.URL = url
		}

		engineClient, err := engine.New(cfg.Engine.URL, cfg.Engine.APIKey, cfg.Engine.Timeout)
		if err != nil {
			return fmt.Errorf("failed to create engine client: %w", err)
		}

		if err := engineClient.HealthCheck(cmd.Context()); err != nil {
			cmd.Printf("⚠️  Warning: Engine health check failed: %v\n\n", err)
			// Continue anyway — user might be offline but have cached data.
		}

		verbose, _ := cmd.Flags().GetBool("verbose")
		logger := utils.NewLogger(&cfg.Logging, verbose)

		// Auto-detect K8s version if not provided
		if k8sVersion == "" {
			kubeClient, err := client.NewKubeClient(&cfg.Cluster)
			if err != nil {
				return fmt.Errorf("failed to create kubernetes client: %w", err)
			}
			k8sVersion, err = client.GetClusterK8sVersion(kubeClient)
			if err != nil {
				return fmt.Errorf("failed to detect cluster K8s version (use --target-k8s-version to override): %w", err)
			}
			cmd.Printf("Detected cluster Kubernetes version: %s\n\n", k8sVersion)
		}

		// Run shared cluster scan
		gitopsProvider, _ := cmd.Flags().GetString("gitops")
		gitopsNamespace, _ := cmd.Flags().GetString("gitops-namespace")

		result, err := scanner.RunClusterScan(cmd.Context(), scanner.ClusterScanParams{
			Config:          cfg,
			Kubeconfig:      kubeconfig,
			Logger:          logger,
			GitOpsProvider:  gitopsProvider,
			GitOpsNamespace: gitopsNamespace,
			ScanTypes:       []string{"helm"},
			K8sVersion:      k8sVersion,
			Version:         Version,
		})
		if err != nil {
			return err
		}

		// Applied before the compat-check loop below (not just before
		// output like FilterResources/SortResources) so a scoped
		// --destination-cluster run doesn't spend engine calls on
		// resources bound for a different cluster.
		destCluster, _ := cmd.Flags().GetString("destination-cluster")
		result.Resources = output.FilterByDestination(result.Resources, destCluster)

		// Check compatibility for each deployed chart
		breaking := 0
		for i, r := range result.Resources {
			resp, err := engineClient.CheckCompat(cmd.Context(), r.Name, r.CurrentVersion, k8sVersion)
			if err != nil {
				result.Resources[i].CompatStatus = types.CompatStatusUnknown
				result.Resources[i].TargetVersion = k8sVersion
				result.Resources[i].MigrationAction = fmt.Sprintf("engine error: %v", err)
				breaking++
				continue
			}

			if applyClusterCompatResult(&result.Resources[i], resp, k8sVersion) {
				breaking++
			}
			if resp.Status == "not_tracked" {
				// Best-effort discovery telemetry — unlike compat_chart,
				// every scanner (Helm/ArgoCD/Flux) already populates
				// Repository, so this has real repo data to report.
				_ = engineClient.LogDiscovered(cmd.Context(), engine.DiscoveredRequest{
					ChartName:      r.Name,
					Repository:     r.Repository,
					CurrentVersion: r.CurrentVersion,
					Status:         "discovered",
				})
			}
		}

		// Apply filters before output
		filters, _ := cmd.Flags().GetStringSlice("filter")
		securityOnly, _ := cmd.Flags().GetBool("security-only")
		result.Resources = output.FilterResources(result.Resources, filters, securityOnly)

		// Apply sorting
		sortBy, _ := cmd.Flags().GetString("sort-by")
		result.Resources = output.SortResources(result.Resources, sortBy)

		// Update statistics
		result.Statistics.TotalResources = len(result.Resources)
		result.Statistics.CurrentResources = 0
		result.Statistics.UpdatesAvailable = 0
		result.Statistics.SecurityIssues = 0
		for _, r := range result.Resources {
			if r.CompatStatus == types.CompatStatusCompatible {
				result.Statistics.CurrentResources++
			} else {
				result.Statistics.UpdatesAvailable++
			}
		}

		// Output using shared formatter
		outputFormat, _ := cmd.Flags().GetString("output")
		exportPath, _ := cmd.Flags().GetString("export")

		if err := output.FormatTo(result, outputFormat, exportPath); err != nil {
			return fmt.Errorf("failed to format output: %w", err)
		}

		// Summary message for table output
		if outputFormat == "table" && exportPath == "" {
			output.PrintTransparencyNotes(result.Resources)
			if breaking > 0 {
				fmt.Printf("\n⚠️  %d chart(s) will break on K8s %s\n", breaking, k8sVersion)
			} else {
				fmt.Printf("\n✅ All charts compatible with K8s %s\n", k8sVersion)
			}
		}

		return nil
	},
}

func init() {
	compatCmd.AddCommand(compatClusterCmd)

	compatClusterCmd.Flags().String("target-k8s-version", "", "Target Kubernetes version (auto-detected from cluster if not provided)")
	compatClusterCmd.Flags().String("gitops", "", "GitOps provider (argocd, flux, all, none) — 'all' includes helm releases too, same as 'kubuto scan --all'")
	compatClusterCmd.Flags().String("gitops-namespace", "", "Override default namespace for GitOps provider")
	compatClusterCmd.Flags().String("engine-url", "", "Override engine URL from config")
	compatClusterCmd.Flags().StringP("output", "o", "table", "Output format (table, json, yaml)")
	compatClusterCmd.Flags().String("export", "", "Export results to file")
	compatClusterCmd.Flags().String("sort-by", "name", "Sort results by field (name,namespace,status,risk,destination)")
	compatClusterCmd.Flags().StringSlice("filter", []string{}, "Filter results (current,patch,minor,major,deprecated)")
	compatClusterCmd.Flags().Bool("security-only", false, "Show only resources with security updates")
	compatClusterCmd.Flags().BoolP("verbose", "v", false, "Verbose output with debug information")
	compatClusterCmd.Flags().String("destination-cluster", "", "Restrict ArgoCD-sourced rows to one destination cluster (matches spec.destination.server/.name or .namespace)")
}

// applyClusterCompatResult writes a successful engine compat response
// onto a resource, mirroring what `compat chart`/`compat release` already
// show via displayCompatResult (including staleness and source, which
// this command previously dropped). Returns true if the resource is
// incompatible, so the caller can track a running "breaking" count.
func applyClusterCompatResult(res *types.Resource, resp *engine.CompatResponse, k8sVersion string) bool {
	if resp.Compatible {
		res.CompatStatus = types.CompatStatusCompatible
	} else {
		res.CompatStatus = types.CompatStatusIncompatible
	}
	res.TargetVersion = k8sVersion
	res.CompatStale = resp.IsStale
	res.CompatSource = resp.Source

	switch {
	case resp.Compatible:
		res.MigrationAction = "compatible"
	case resp.Status == "no_data":
		res.MigrationAction = fmt.Sprintf("no compatibility data for %s %s", res.Name, res.CurrentVersion)
	case resp.Status == "not_tracked":
		res.MigrationAction = fmt.Sprintf("chart %s not tracked by engine", res.Name)
	default:
		res.MigrationAction = fmt.Sprintf("incompatible with K8s %s", k8sVersion)
	}

	return !resp.Compatible
}
