package scanner

import (
	"context"
	"fmt"

	"github.com/akshatsinha007/kubuto/internal/client"
	"github.com/akshatsinha007/kubuto/internal/config"
	"github.com/akshatsinha007/kubuto/internal/utils"
	"github.com/akshatsinha007/kubuto/pkg/types"
	"github.com/go-logr/logr"
)

// GitOpsProviderAll is the sentinel value for `kubuto scan --all`. When
// passed to RunClusterScan it instantiates *both* the ArgoCD and Flux
// scanners (in addition to whichever entries appear in ScanTypes), giving
// users a single-command sweep over every Helm-shaped resource the
// cluster can deploy. Image scanners are deliberately excluded — `--all`
// is about chart current↔latest, not container image tags.
const GitOpsProviderAll = "all"

// ClusterScanParams contains all parameters needed to run a cluster scan.
// This is the shared discovery step used by both `scan` and
// `compat cluster` — the engine itself is never passed in here or to any
// scanner constructor, so `compat cluster` runs its own compat lookups
// against the resources this returns, after the fact.
type ClusterScanParams struct {
	Config          *config.Config
	Kubeconfig      string
	Logger          logr.Logger
	GitOpsProvider  string
	GitOpsNamespace string
	ScanTypes       []string
	K8sVersion      string
	Version         string // kubuto's own build version, stamped into ScanResult.Metadata.KubutoVersion
}

// RunClusterScan creates scanners, validates them, and executes a cluster scan.
// This is the shared logic used by both `scan` and `compat cluster` commands.
func RunClusterScan(ctx context.Context, params ClusterScanParams) (*types.ScanResult, error) {
	cfg := params.Config

	// Override kubeconfig if provided
	if params.Kubeconfig != "" {
		cfg.Cluster.Kubeconfig = params.Kubeconfig
	}

	kubeClient, err := client.NewKubeClient(&cfg.Cluster)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	logger := params.Logger
	if (logr.Logger{}) == logger {
		logger = utils.NewLogger(&cfg.Logging, false)
	}

	// Create base scanners
	artifactHubClient := client.NewArtifactHubClient()
	helmClient := client.NewHelmClient()
	registryClient := client.NewRegistryClient(cfg)

	helmScanner := NewHelmScanner(kubeClient, artifactHubClient, helmClient, logger.WithName("helm-scanner"))
	imageScanner := NewImageScanner(kubeClient, registryClient, logger.WithName("image-scanner"))

	scanners := []Scanner{}
	for _, scanType := range params.ScanTypes {
		switch scanType {
		case "helm":
			scanners = append(scanners, helmScanner)
		case "image", "images":
			scanners = append(scanners, imageScanner)
		}
	}

	// Decide which GitOps scanners to instantiate. We accept three
	// concrete values here:
	//   - "argocd": ArgoCD only
	//   - "flux"  : Flux only
	//   - "all"   : both (driven by `kubuto scan --all`)
	// Anything else (incl. "" and "none") skips GitOps entirely.
	wantArgo, wantFlux := false, false
	switch params.GitOpsProvider {
	case "argocd":
		wantArgo = true
	case "flux":
		wantFlux = true
	case GitOpsProviderAll:
		wantArgo = true
		wantFlux = true
	}

	if wantArgo || wantFlux {
		dynamicClient, err := client.NewDynamicClient(&cfg.Cluster)
		if err != nil {
			return nil, fmt.Errorf("failed to create dynamic client for gitops scan: %w", err)
		}

		// GitOps scanners receive `helmClient`, not an engine client —
		// latest-version resolution happens via direct chart-repo
		// `index.yaml` reads (same as helm.go). Engine calls belong
		// entirely to the compat command path.
		if wantArgo {
			fetchMethod := cfg.Scanning.ArgoCD.FetchMethod
			if fetchMethod == "" {
				fetchMethod = "api"
			}
			argocdScanner := NewArgoCDScanner(kubeClient, dynamicClient, helmClient, fetchMethod, logger.WithName("argocd-scanner"))
			scanners = append(scanners, argocdScanner)
		}
		if wantFlux {
			fluxScanner := NewFluxScanner(kubeClient, dynamicClient, helmClient, logger.WithName("flux-scanner"))
			scanners = append(scanners, fluxScanner)
		}
	}

	// Resolve gitops configs
	var gitopsConfigs []config.GitOpsRepo
	if params.GitOpsProvider != "" && params.GitOpsProvider != "none" {
		for _, g := range cfg.Scanning.ArgoCD.GitOps {
			gitopsConfigs = append(gitopsConfigs, g)
		}
	}

	// Build coordinator
	coordinator := &ScanCoordinator{
		Scanners: scanners,
		Client:   kubeClient,
		Config:   cfg,
		Logger:   logger,
	}

	// Validate scanners
	for _, s := range coordinator.Scanners {
		if err := s.Validate(ctx); err != nil {
			return nil, fmt.Errorf("failed to validate scanner %s: %w", s.Name(), err)
		}
	}

	// Execute scans
	scanOpts := ScanOptions{
		Namespaces:      cfg.Scanning.Namespaces.Include,
		GitOpsProvider:  params.GitOpsProvider,
		GitOpsNamespace: params.GitOpsNamespace,
		K8sVersion:      params.K8sVersion,
		GitOpsConfigs:   gitopsConfigs,
	}

	result, err := coordinator.ExecuteScans(ctx, scanOpts)
	if err != nil {
		return nil, fmt.Errorf("scan failed: %w", err)
	}

	result.Metadata.KubutoVersion = params.Version

	return result, nil
}
