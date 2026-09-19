package cmd

import (
	"fmt"
	"time"

	"github.com/akshatsinha007/kubuto/internal/client"
	"github.com/akshatsinha007/kubuto/internal/config"
	"github.com/akshatsinha007/kubuto/internal/output"
	"github.com/akshatsinha007/kubuto/internal/scanner"
	"github.com/akshatsinha007/kubuto/internal/utils"
	"github.com/spf13/cobra"
)

var scanCmd = &cobra.Command{
	Use:   "scan",
	Short: "Scan cluster resources for version updates",
	Long: `Discover deployed resources and report current vs latest available versions.

This command answers "what is deployed and what newer versions exist?".
It NEVER calls the Kubuto engine — every lookup is a direct
` + "`index.yaml`" + ` fetch against the chart repository each resource was
installed from. No API key, no quota burn, no network egress to Kubuto.

For Kubernetes-version compatibility ("will my chart still work after I
upgrade K8s?") use ` + "`kubuto compat ...`" + ` instead.

Examples:
  kubuto scan                                # helm releases (cluster-wide)
  kubuto scan --type helm,image              # helm + container images
  kubuto scan --all                          # helm + flux + argocd (NO images)
  kubuto scan --gitops argocd                # ONLY argocd applications
  kubuto scan --gitops flux                  # ONLY flux helmreleases
  kubuto scan -n devtroncd,default           # restrict to two namespaces
  kubuto scan --gitops argocd -n devtroncd   # argocd apps in devtroncd only
  kubuto scan --type helm --gitops argocd    # both helm + argocd (explicit opt-in)`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.LoadConfig(cfgFile)
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		verbose, _ := cmd.Flags().GetBool("verbose")
		logger := utils.NewLogger(&cfg.Logging, verbose)
		logger.Info("Starting scan")

		scanTypes, _ := cmd.Flags().GetStringSlice("type")
		typeFlagSet := cmd.Flags().Changed("type")

		// Validate --type values up-front. The dispatcher in
		// `RunClusterScan` (cluster_scan.go) silently no-ops on unknown
		// values, which previously caused `kubuto scan --type gitops`
		// to print an empty table with exit code 0 — indistinguishable
		// from a working command on a quiet cluster. Reject unknowns
		// with a message that points at the right flag (`--gitops` /
		// `--all`) so the user can self-correct without reading source.
		if typeFlagSet {
			for _, t := range scanTypes {
				if !scanner.IsValidScanType(t) {
					return fmt.Errorf(
						"--type %q is not a valid resource type.\n"+
							"  Valid values: %v.\n"+
							"  To scan ArgoCD or Flux sources use --gitops argocd|flux,\n"+
							"  or --all to combine helm + GitOps in one pass.",
						t, scanner.ValidScanTypes)
				}
			}
		}

		gitopsProvider, _ := cmd.Flags().GetString("gitops")
		gitopsNamespace, _ := cmd.Flags().GetString("gitops-namespace")
		namespaceFlag, _ := cmd.Flags().GetStringSlice("namespace")
		k8sVersion, _ := cmd.Flags().GetString("k8s-version")
		batchSize, _ := cmd.Flags().GetInt("batch-size")
		allFlag, _ := cmd.Flags().GetBool("all")

		// Global git credential fallback — used only when a git source's
		// repo has no matching ArgoCD secret and no per-repo/per-prefix
		// `scanning.argocd.gitops` entry either. Covers the common case
		// of one PAT/token that's valid for every repo under a git host
		// or group (e.g. a whole GitLab group of GitOps-generated repos)
		// without requiring one config entry per repo.
		gitToken, _ := cmd.Flags().GetString("git-token")
		gitUsername, _ := cmd.Flags().GetString("git-username")
		gitPassword, _ := cmd.Flags().GetString("git-password")
		defaultGitAuth := client.GitAuth{Token: gitToken, Username: gitUsername, Password: gitPassword}

		// `--all` is a convenience for the dominant use case
		// "tell me everything that has a current↔latest mapping". It is
		// strictly a *superset* in the chart dimension (helm + flux +
		// argocd) and a strict *subset* in the type dimension (it
		// excludes images, which is a different lookup pipeline against
		// container registries). Mutually exclusive with explicit
		// --gitops because mixing them is always either redundant
		// (--all --gitops argocd) or contradictory (--all --gitops none).
		if allFlag {
			if gitopsProvider != "" && gitopsProvider != scanner.GitOpsProviderAll {
				return fmt.Errorf("--all and --gitops are mutually exclusive (use --gitops alone if you want a single provider)")
			}
			scanTypes = []string{"helm"}
			gitopsProvider = scanner.GitOpsProviderAll
		}

		// `--gitops X` (without --all and without an explicit --type)
		// is a pure GitOps mode, not "helm + GitOps". The doc string on
		// the flag has always promised "only argocd applications" /
		// "only flux helmreleases"; previously the default --type=helm
		// silently appended a 200+ row helm scan on top, drowning the
		// GitOps rows. If a user wants the additive shape they can
		// pass --type explicitly (`--type helm --gitops argocd`) or
		// use --all.
		if gitopsProvider != "" && gitopsProvider != "none" &&
			gitopsProvider != scanner.GitOpsProviderAll && !typeFlagSet {
			scanTypes = nil
		}

		// `--namespace ns1,ns2` overrides the include list from the
		// config file. We push it onto cfg.Scanning.Namespaces.Include
		// (rather than only ScanOptions.Namespaces) so any code path
		// that reads from the config object — current or future —
		// observes a single source of truth. RunClusterScan then
		// forwards this to ScanOptions.Namespaces just as before.
		if len(namespaceFlag) > 0 {
			cfg.Scanning.Namespaces.Include = namespaceFlag
		}

		// Update config with command-line value if provided
		cfg.Scanning.BatchSize = batchSize

		// Auto-detect K8s version if not provided
		if k8sVersion == "" {
			kubeClient, err := client.NewKubeClient(&cfg.Cluster)
			if err == nil {
				k8sVersion, err = client.GetClusterK8sVersion(kubeClient)
				if err != nil {
					logger.V(1).Info("Failed to detect K8s version", "error", err)
				} else {
					logger.V(1).Info("Auto-detected K8s version", "version", k8sVersion)
				}
			}
		}

		// Run shared cluster scan
		result, err := scanner.RunClusterScan(cmd.Context(), scanner.ClusterScanParams{
			Config:          cfg,
			Kubeconfig:      kubeconfig,
			Logger:          logger,
			GitOpsProvider:  gitopsProvider,
			GitOpsNamespace: gitopsNamespace,
			ScanTypes:       scanTypes,
			K8sVersion:      k8sVersion,
			Version:         Version,
			DefaultGitAuth:  defaultGitAuth,
		})
		if err != nil {
			return err
		}

		// Apply filters
		filters, _ := cmd.Flags().GetStringSlice("filter")
		securityOnly, _ := cmd.Flags().GetBool("security-only")
		result.Resources = output.FilterResources(result.Resources, filters, securityOnly)

		destCluster, _ := cmd.Flags().GetString("destination-cluster")
		result.Resources = output.FilterByDestination(result.Resources, destCluster)

		// Apply sorting
		sortBy, _ := cmd.Flags().GetString("sort-by")
		result.Resources = output.SortResources(result.Resources, sortBy)

		// Update statistics after filtering
		result.Statistics.TotalResources = len(result.Resources)
		result.Statistics.CurrentResources = 0
		result.Statistics.UpdatesAvailable = 0
		result.Statistics.SecurityIssues = 0
		for _, r := range result.Resources {
			switch r.UpdateStatus {
			case "current":
				result.Statistics.CurrentResources++
			default:
				result.Statistics.UpdatesAvailable++
			}
			if r.SecurityRisk != "none" && r.SecurityRisk != "" {
				result.Statistics.SecurityIssues++
			}
		}

		outputFormat, _ := cmd.Flags().GetString("output")
		exportPath, _ := cmd.Flags().GetString("export")

		if err := output.FormatTo(result, outputFormat, exportPath); err != nil {
			return fmt.Errorf("failed to format output: %w", err)
		}

		if outputFormat == "table" && exportPath == "" {
			output.PrintTransparencyNotes(result.Resources)
		}

		logger.Info("Scan finished")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(scanCmd)

	scanCmd.Flags().StringSlice("type", []string{"helm"}, "Resource types to scan (helm, image)")
	scanCmd.Flags().Bool("all", false, "Scan helm + flux + argocd in one pass (excludes images; mutually exclusive with --gitops)")
	scanCmd.Flags().StringP("output", "o", "table", "Output format (table, json, yaml)")
	scanCmd.Flags().StringSlice("filter", []string{}, "Filter results (current,patch,minor,major,deprecated)")
	scanCmd.Flags().String("sort-by", "name", "Sort results by field (name,namespace,status,risk,destination)")
	scanCmd.Flags().Bool("security-only", false, "Show only resources with security updates")
	scanCmd.Flags().String("export", "", "Export results to file")
	scanCmd.Flags().Bool("watch", false, "Watch for changes and re-scan periodically")
	scanCmd.Flags().Bool("dry-run", false, "Show what would be scanned without actually scanning")
	scanCmd.Flags().Duration("timeout", 5*time.Minute, "Timeout for scan operation")
	scanCmd.Flags().Int("concurrency", 10, "Number of concurrent operations")
	scanCmd.Flags().Int("batch-size", 5, "Number of scanners to run in each batch")
	scanCmd.Flags().Bool("no-cache", false, "Disable caching for this scan")
	scanCmd.Flags().BoolP("quiet", "q", false, "Suppress progress output")
	scanCmd.Flags().BoolP("verbose", "v", false, "Verbose output with debug information")
	scanCmd.Flags().StringSliceP("namespace", "n", nil, "Restrict scan to specific namespaces (comma-separated or repeated). Defaults to cluster-wide.")
	scanCmd.Flags().String("gitops", "", "Scan a single GitOps source exclusively (argocd, flux, none). Implies --type=''; pass --type explicitly to combine.")
	scanCmd.Flags().String("gitops-namespace", "", "Constrain GitOps scan to a single namespace (overrides --namespace for the GitOps scanner only)")
	scanCmd.Flags().String("k8s-version", "", "Override auto-detected K8s version")
	scanCmd.Flags().String("destination-cluster", "", "Restrict ArgoCD-sourced rows to one destination cluster (matches spec.destination.server/.name or .namespace)")
	scanCmd.Flags().String("git-token", "", "Fallback git credential (bearer/PAT) for git-source repos with no matching ArgoCD secret or scanning.argocd.gitops entry")
	scanCmd.Flags().String("git-username", "", "Fallback git username, paired with --git-password (used only if --git-token is empty)")
	scanCmd.Flags().String("git-password", "", "Fallback git password/PAT, paired with --git-username (used only if --git-token is empty)")
}
