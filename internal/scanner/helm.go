package scanner

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/go-logr/logr"
	"helm.sh/helm/v3/pkg/release"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/akshatsinha007/kubuto/internal/client"
	"github.com/akshatsinha007/kubuto/internal/version"
	"github.com/akshatsinha007/kubuto/pkg/types"
)

const (
	// artifactHubRepoAnnotation is a common annotation key used by Helm to store the repository name.
	artifactHubRepoAnnotation = "artifacthub.io/repository-name"
)

// HelmScanner is a scanner for Helm releases.
type HelmScanner struct {
	kubeClient        kubernetes.Interface
	artifactHubClient *client.ArtifactHubClient
	helmClient        *client.HelmClient
	logger            logr.Logger
}

// NewHelmScanner creates a new Helm scanner with its dependencies.
func NewHelmScanner(kc kubernetes.Interface, ahc *client.ArtifactHubClient, hc *client.HelmClient, logger logr.Logger) *HelmScanner {
	return &HelmScanner{
		kubeClient:        kc,
		artifactHubClient: ahc,
		helmClient:        hc,
		logger:            logger,
	}
}

// Name returns the name of the scanner.
func (s *HelmScanner) Name() string {
	return "Helm"
}

// Scan discovers Helm releases in the cluster and checks for updates.
func (s *HelmScanner) Scan(ctx context.Context, opts ScanOptions) ([]types.Resource, error) {
	var resources []types.Resource

	namespaces := opts.Namespaces
	if len(namespaces) == 0 {
		namespaces = []string{v1.NamespaceAll}
	}

	for _, ns := range namespaces {
		secrets, err := paginatedSecretList(ctx, s.kubeClient.CoreV1().Secrets(ns).List, metav1.ListOptions{
			LabelSelector: "owner=helm",
		})
		if err != nil {
			return nil, fmt.Errorf("failed to list helm secrets in namespace %s: %w", ns, err)
		}

		var deployed []*release.Release
		for _, secret := range secrets {
			rel, err := s.decodeRelease(secret.Data["release"])
			if err != nil {
				s.logger.V(1).Info("Error decoding release from secret", "namespace", secret.Namespace, "name", secret.Name, "error", err)
				continue
			}

			if rel.Info.Status != release.StatusDeployed {
				continue
			}

			deployed = append(deployed, rel)
		}

		// processRelease does real network lookups (ArtifactHub, chart
		// repo index.yaml) — run up to scanConcurrency of them at once
		// instead of one release at a time. Decoding/status-filtering
		// above stays sequential since it's cheap and purely local.
		resources = append(resources, parallelMap(deployed, scanConcurrency, func(rel *release.Release) types.Resource {
			return s.processRelease(ctx, rel, opts)
		})...)
	}

	return resources, nil
}

// processRelease takes a single Helm release and returns a populated Resource.
func (s *HelmScanner) processRelease(ctx context.Context, rel *release.Release, opts ScanOptions) types.Resource {
	chartName := rel.Chart.Metadata.Name
	currentVersionStr := rel.Chart.Metadata.Version

	res := types.Resource{
		Name:           rel.Name,
		Namespace:      rel.Namespace,
		Type:           types.ResourceTypeHelmChart,
		CurrentVersion: currentVersionStr,
		UpdateStatus:   types.UpdateStatusUnknown, // Default status
	}

	currentVersion, err := version.Parse(currentVersionStr)
	if err != nil {
		s.logger.V(1).Info("Error parsing current version for release", "release", res.Name, "version", currentVersionStr, "error", err)
		return res
	}

	// Strategy 1: Check for Artifact Hub annotation for the repository name.
	if repoName, ok := rel.Chart.Metadata.Annotations[artifactHubRepoAnnotation]; ok {
		s.logger.V(1).Info("Strategy 1: Found chart in Artifact Hub via annotation", "release", res.Name, "repo", repoName)
		res.Repository = fmt.Sprintf("artifacthub://%s", repoName)
		versions, err := s.artifactHubClient.GetChartVersions(ctx, repoName, chartName)
		if err != nil {
			s.logger.V(1).Info("Error getting versions from Artifact Hub", "repo", repoName, "chart", chartName, "error", err)
			return res // Return with unknown status
		}
		s.analyzeVersions(&res, currentVersion, versions)
		return res
	}

	// Strategy 2: Check the 'sources' field in Chart.yaml.
	s.logger.V(1).Info("Strategy 2: Checking 'sources' field in Chart.yaml", "release", res.Name)
	for _, sourceURL := range rel.Chart.Metadata.Sources {
		repoURL := sourceURL
		// Handle index.yaml URLs
		if strings.HasSuffix(repoURL, "/index.yaml") {
			repoURL = strings.TrimSuffix(repoURL, "/index.yaml")
		}

		// Handle GitHub URLs -> GitHub Pages
		if strings.HasPrefix(repoURL, "https://github.com/") {
			parts := strings.Split(strings.TrimPrefix(repoURL, "https://github.com/"), "/")
			if len(parts) >= 2 {
				repoURL = fmt.Sprintf("https://%s.github.io/%s", parts[0], parts[1])
			}
		}

		s.logger.V(1).Info("Attempting to find chart in source repository", "originalSource", sourceURL, "derivedRepoURL", repoURL, "chart", chartName)
		versions, err := s.helmClient.GetChartVersions(ctx, repoURL, chartName)
		if err == nil {
			s.logger.V(1).Info("Found chart in source repository", "repoURL", repoURL)
			res.Repository = repoURL
			s.analyzeVersions(&res, currentVersion, versions)
			return res
		}
	}

	// Strategy 3: Check configured custom repositories by URL.
	s.logger.V(1).Info("Strategy 3: Checking configured custom repositories", "release", res.Name)
	if opts.Config != nil && opts.Config.Scanning.Helm.CustomRepos != nil {
		for _, customRepo := range opts.Config.Scanning.Helm.CustomRepos {
			s.logger.V(1).Info("Attempting to find chart in custom repository", "repoURL", customRepo.URL, "chart", chartName)
			versions, err := s.helmClient.GetChartVersions(ctx, customRepo.URL, chartName)
			if err == nil {
				// Found it!
				res.Repository = customRepo.URL
				s.analyzeVersions(&res, currentVersion, versions)
				return res // Stop after finding the first match.
			}
		}
	}

	// Strategy 4: Fallback to Artifact Hub search
	s.logger.V(1).Info("Strategy 4: Falling back to Artifact Hub search", "release", res.Name, "chart", chartName)
	repoName, officialChartName, err := s.artifactHubClient.SearchChart(ctx, chartName)
	if err == nil {
		s.logger.V(1).Info("Found chart on Artifact Hub", "chart", chartName, "repo", repoName)
		versions, err := s.artifactHubClient.GetChartVersions(ctx, repoName, officialChartName)
		if err == nil {
			res.Repository = fmt.Sprintf("artifacthub://%s", repoName)
			s.analyzeVersions(&res, currentVersion, versions)
			return res
		} else {
			s.logger.V(1).Info("Error getting versions from Artifact Hub after successful search", "repo", repoName, "chart", officialChartName, "error", err)
		}
	}

	s.logger.V(1).Info("Could not determine repository for chart", "chart", chartName, "release", res.Name)
	return res
}

// analyzeVersions performs the version comparison and populates the resource.
func (s *HelmScanner) analyzeVersions(res *types.Resource, currentVer *semver.Version, availableStr []string) {
	s.logger.V(1).Info("Analyzing versions", "release", res.Name, "availableVersions", availableStr)
	availableVers := version.StringsToVersions(availableStr)
	latest, latestMinor, latestPatch := version.Resolve(currentVer, availableVers)

	if latest != nil {
		res.LatestVersion = latest.String()
		res.UpdateStatus = version.Compare(currentVer, latest)
	} else {
		res.UpdateStatus = types.UpdateStatusCurrent
	}

	if latestMinor != nil {
		res.LatestMinor = latestMinor.String()
	}
	if latestPatch != nil {
		res.LatestPatch = latestPatch.String()
	}
}

// Validate checks if the scanner can run.
func (s *HelmScanner) Validate(ctx context.Context) error {
	if s.kubeClient == nil {
		return fmt.Errorf("kubernetes client is not initialized")
	}
	if s.artifactHubClient == nil {
		return fmt.Errorf("artifact hub client is not initialized")
	}
	if s.helmClient == nil {
		return fmt.Errorf("helm client is not initialized")
	}
	return nil
}

// Cleanup performs any cleanup actions.
func (s *HelmScanner) Cleanup() error {
	return nil
}

// decodeRelease decodes the gzipped, base64-encoded release data from a secret.
func (s *HelmScanner) decodeRelease(data []byte) (*release.Release, error) {
	b, err := base64.StdEncoding.DecodeString(string(data))
	if err != nil {
		return nil, fmt.Errorf("failed to base64 decode release data: %w", err)
	}

	r, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer r.Close()

	d, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("failed to decompress release data: %w", err)
	}

	var rel release.Release
	if err := json.Unmarshal(d, &rel); err != nil {
		return nil, fmt.Errorf("failed to unmarshal release JSON: %w", err)
	}
	return &rel, nil
}
