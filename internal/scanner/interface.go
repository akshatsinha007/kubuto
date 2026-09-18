package scanner

import (
	"context"
	"time"

	"fmt"

	"github.com/akshatsinha007/kubuto/internal/cache"
	"github.com/akshatsinha007/kubuto/internal/config"
	"github.com/akshatsinha007/kubuto/pkg/types"
	"github.com/go-logr/logr"
	"k8s.io/client-go/kubernetes"
)

// ValidScanTypes is the canonical, ordered list of values accepted by
// `kubuto scan --type`. It is intentionally narrow: it represents the
// resource *kind* dimension only (helm releases vs container images),
// not the GitOps-source dimension. ArgoCD / Flux are selected via the
// separate `--gitops` flag, and `--all` combines both axes. Keeping this
// list in one place lets `cmd/scan.go` validate the flag up-front and
// produce a self-explaining error instead of silently dropping invalid
// values into a no-match scan, which previously surfaced as an empty
// table and zero exit code (genuinely confusing for `--type gitops`).
var ValidScanTypes = []string{"helm", "image"}

// IsValidScanType reports whether v matches one of ValidScanTypes. The
// "images" alias is also accepted for backwards compatibility with any
// existing config files that pluralised the value before the canonical
// form was settled.
func IsValidScanType(v string) bool {
	if v == "images" {
		return true
	}
	for _, t := range ValidScanTypes {
		if v == t {
			return true
		}
	}
	return false
}

// Scanner defines the interface for all resource scanners
type Scanner interface {
	// Name returns the scanner identifier
	Name() string

	// Scan performs the resource discovery and version checking
	Scan(ctx context.Context, opts ScanOptions) ([]types.Resource, error)

	// Validate checks if the scanner can operate in the current environment
	Validate(ctx context.Context) error

	// Cleanup performs any necessary cleanup operations
	Cleanup() error
}

// ScanOptions provides options for a scan operation.
type ScanOptions struct {
	Namespaces      []string
	LabelSelectors  []string
	FieldSelectors  []string
	IncludePrivate  bool
	Timeout         time.Duration
	Concurrency     int
	Cache           bool
	Config          *config.Config
	GitOpsProvider  string // argocd, flux, none
	GitOpsNamespace string // override default namespace
	K8sVersion      string
	GitOpsConfigs   []config.GitOpsRepo
}

// resolveTargetNamespaces resolves the effective set of namespaces a
// GitOps scanner (ArgoCD, Flux) should walk, in order of precedence:
// a single `GitOpsNamespace` override, then the general `Namespaces`
// list, then cluster-wide (returned as one empty-string entry, which
// the dynamic client treats as "all namespaces"). Always returns at
// least one element so callers never need to special-case the
// cluster-wide path.
func resolveTargetNamespaces(opts ScanOptions) []string {
	if opts.GitOpsNamespace != "" {
		return []string{opts.GitOpsNamespace}
	}
	if len(opts.Namespaces) > 0 {
		// Defensive copy so a downstream mutation of the returned slice
		// can never bleed back into ScanOptions.
		out := make([]string, 0, len(opts.Namespaces))
		out = append(out, opts.Namespaces...)
		return out
	}
	return []string{""}
}

// ScanCoordinator coordinates multiple scanners
type ScanCoordinator struct {
	Scanners []Scanner
	Client   kubernetes.Interface
	Config   *config.Config
	Cache    cache.Interface
	Logger   logr.Logger
}

// ExecuteScans executes all registered scanners and aggregates the results.
func (sc *ScanCoordinator) ExecuteScans(ctx context.Context, opts ScanOptions) (*types.ScanResult, error) {
	var allResources []types.Resource
	var allErrors []types.ScanError

	// Pass the config from the coordinator to the options for the scanners to use.
	opts.Config = sc.Config

	// Determine batch size from config, default to 5 if not set or invalid
	batchSize := 5
	if sc.Config != nil && sc.Config.Scanning.BatchSize > 0 {
		batchSize = sc.Config.Scanning.BatchSize
	}

	// Process scanners in batches
	for i := 0; i < len(sc.Scanners); i += batchSize {
		end := i + batchSize
		if end > len(sc.Scanners) {
			end = len(sc.Scanners)
		}

		batch := sc.Scanners[i:end]

		// Process current batch
		for _, s := range batch {
			resources, err := s.Scan(ctx, opts)
			if err != nil {
				allErrors = append(allErrors, types.ScanError{Message: fmt.Sprintf("scanner %s failed: %v", s.Name(), err)})
				continue
			}
			allResources = append(allResources, resources...)
		}
	}

	result := &types.ScanResult{
		Resources: allResources,
		Errors:    allErrors,
		// Metadata and Statistics will be populated later
	}

	return result, nil
}
