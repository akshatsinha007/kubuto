package scanner

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/akshatsinha007/kubuto/internal/client"
	"github.com/akshatsinha007/kubuto/pkg/types"
)

var (
	// Flux HelmRelease CRD (helm.toolkit.fluxcd.io/v2)
	helmReleaseGVR = schema.GroupVersionResource{
		Group:    "helm.toolkit.fluxcd.io",
		Version:  "v2",
		Resource: "helmreleases",
	}
	// Fallback to v2beta1 for older Flux installations
	helmReleaseGVRv2beta1 = schema.GroupVersionResource{
		Group:    "helm.toolkit.fluxcd.io",
		Version:  "v2beta1",
		Resource: "helmreleases",
	}
	// Flux GitRepository CRD (source.toolkit.fluxcd.io/v1)
	gitRepositoryGVR = schema.GroupVersionResource{
		Group:    "source.toolkit.fluxcd.io",
		Version:  "v1",
		Resource: "gitrepositories",
	}
	// Flux HelmRepository CRD (source.toolkit.fluxcd.io/v1)
	helmRepositoryGVR = schema.GroupVersionResource{
		Group:    "source.toolkit.fluxcd.io",
		Version:  "v1",
		Resource: "helmrepositories",
	}
	// Flux OCIRepository CRD (source.toolkit.fluxcd.io/v1)
	ociRepositoryGVR = schema.GroupVersionResource{
		Group:    "source.toolkit.fluxcd.io",
		Version:  "v1",
		Resource: "ocirepositories",
	}
	// Flux HelmChart CRD (source.toolkit.fluxcd.io/v1) — referenced by
	// HelmRelease v2's spec.chartRef when kind: HelmChart.
	helmChartGVR = schema.GroupVersionResource{
		Group:    "source.toolkit.fluxcd.io",
		Version:  "v1",
		Resource: "helmcharts",
	}
)

// FluxScanner scans Flux CD HelmRelease resources.
//
// Boundary contract: this scanner is part of the `kubuto scan` path. It
// MUST NOT call the Kubuto engine — that's the job of `kubuto compat`.
// We deliberately do not hold an `*engine.Client` here so the import
// graph itself prevents reintroducing engine calls. Compatibility checks
// against a target Kubernetes version live in `cmd/compat_cluster.go`,
// which calls the engine directly after running the scan.
type FluxScanner struct {
	kubeClient    kubernetes.Interface
	dynamicClient dynamic.Interface
	helmClient    *client.HelmClient // for index.yaml fetches; latest-version resolution
	logger        logr.Logger
}

// NewFluxScanner creates a new Flux scanner.
//
// `helmClient` is required for the LatestVersion column to be populated;
// callers may pass nil only in test scenarios that explicitly assert the
// "no upstream lookup" path. Production wiring is in `cluster_scan.go`.
func NewFluxScanner(kc kubernetes.Interface, dc dynamic.Interface, hc *client.HelmClient, logger logr.Logger) *FluxScanner {
	return &FluxScanner{
		kubeClient:    kc,
		dynamicClient: dc,
		helmClient:    hc,
		logger:        logger,
	}
}

// Name returns the scanner name.
func (s *FluxScanner) Name() string {
	return "Flux"
}

// Validate checks if the scanner can run.
func (s *FluxScanner) Validate(ctx context.Context) error {
	if s.kubeClient == nil {
		return fmt.Errorf("kubernetes client is not initialized")
	}
	if s.dynamicClient == nil {
		return fmt.Errorf("dynamic client is not initialized")
	}
	return nil
}

// Cleanup performs any cleanup actions.
func (s *FluxScanner) Cleanup() error {
	return nil
}

// Scan discovers Flux HelmReleases and resolves their current↔latest
// chart versions. No K8s-compatibility check is performed here; that is
// the responsibility of `kubuto compat cluster`.
//
// Namespace selection:
//
//  1. `--gitops-namespace foo` (single) wins.
//  2. Otherwise, `--namespace ns1,ns2` is honoured by listing each
//     namespace in turn and concatenating results.
//  3. Otherwise we list cluster-wide, since Flux objects commonly live
//     alongside the workloads they manage rather than in a fixed
//     `flux-system` namespace.
func (s *FluxScanner) Scan(ctx context.Context, opts ScanOptions) ([]types.Resource, error) {
	var resources []types.Resource
	var allErrors []error

	namespaces := resolveTargetNamespaces(opts)

	// Pre-build the HelmRepository URL lookup table once across the
	// effective namespace set so we don't issue one dynamic-client GET
	// per HelmRelease for the common case (multiple releases sharing
	// one HelmRepository source). HelmRepositories typically live in
	// `flux-system` regardless of where the HelmRelease lives, so for
	// cluster-wide and multi-NS scans we collect them all and let the
	// `<ns>/<name>` key disambiguate.
	repoMap := make(map[string]string)
	for _, ns := range namespaces {
		repos, err := s.listHelmRepositories(ctx, ns)
		if err != nil {
			s.logger.V(1).Info("Failed to list HelmRepositories",
				"namespace", scopeForLog(ns), "error", err)
			continue
		}
		for _, repo := range repos {
			url, _, _ := unstructured.NestedString(repo.Object, "spec", "url")
			if url != "" {
				repoMap[fmt.Sprintf("%s/%s", repo.GetNamespace(), repo.GetName())] = url
			}
		}
	}

	// Walk HelmReleases per-namespace. Errors are collected, not fatal —
	// the cluster might have HelmRelease v2 but no HelmRepository CRDs
	// (rare but possible), or the user may have namespace-scoped RBAC
	// on a subset of namespaces.
	for _, ns := range namespaces {
		releases, err := s.listHelmReleases(ctx, ns)
		if err != nil {
			allErrors = append(allErrors, fmt.Errorf("list HelmReleases in %s: %w", scopeForLog(ns), err))
			s.logger.V(1).Info("Failed to list HelmReleases",
				"namespace", scopeForLog(ns), "error", err)
			continue
		}

		// Resolve each release's sourceRef *before* version resolution
		// so that the Repository field is the real upstream URL — not
		// the Flux-only "Kind:ns/name" placeholder. Without this fix
		// the LatestVersion column would always be blank for
		// HelmRelease rows because index.yaml lookup needs a real
		// http(s) URL.
		for _, release := range releases {
			repoURL := s.resolveSourceRef(ctx, release, repoMap)
			res := s.processHelmRelease(ctx, release, opts, repoURL)
			resources = append(resources, res)
		}
	}

	if len(allErrors) > 0 {
		return resources, fmt.Errorf("flux scan completed with %d errors: %v", len(allErrors), allErrors[0])
	}
	return resources, nil
}

// scopeForLog turns the empty-string sentinel into a human-readable
// "cluster-wide" label so logs aren't ambiguous.
func scopeForLog(ns string) string {
	if ns == "" {
		return "cluster-wide"
	}
	return ns
}

// listHelmReleases lists Flux HelmRelease resources.
func (s *FluxScanner) listHelmReleases(ctx context.Context, namespace string) ([]unstructured.Unstructured, error) {
	// Try v2 first, fall back to v2beta1
	items, err := paginatedUnstructuredList(ctx, s.dynamicClient.Resource(helmReleaseGVR).Namespace(namespace).List, metav1.ListOptions{})
	if err != nil {
		s.logger.V(1).Info("HelmRelease v2 not found, trying v2beta1", "error", err)
		items, err = paginatedUnstructuredList(ctx, s.dynamicClient.Resource(helmReleaseGVRv2beta1).Namespace(namespace).List, metav1.ListOptions{})
		if err != nil {
			return nil, err
		}
	}
	return items, nil
}

// listHelmRepositories lists Flux HelmRepository resources.
func (s *FluxScanner) listHelmRepositories(ctx context.Context, namespace string) ([]unstructured.Unstructured, error) {
	items, err := paginatedUnstructuredList(ctx, s.dynamicClient.Resource(helmRepositoryGVR).Namespace(namespace).List, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	return items, nil
}

// processHelmRelease processes a single Flux HelmRelease and returns a
// Resource with its current↔latest version mapping populated. `repoURL`
// is the already-resolved upstream URL (HelmRepository.spec.url or
// GitRepository.spec.url) — passing it in avoids re-resolving the
// sourceRef twice. We keep a placeholder Repository (`Kind:ns/name`)
// only when resolution failed, so the user sees *something* identifying
// the source rather than a blank cell.
//
// Note: GitRepository and oci:// sources fall through `ResolveLatestFromIndex`
// as no-ops; their LatestVersion column stays blank by design.
func (s *FluxScanner) processHelmRelease(ctx context.Context, release unstructured.Unstructured, opts ScanOptions, repoURL string) types.Resource {
	name := release.GetName()
	ns := release.GetNamespace()

	chartName, _, _ := unstructured.NestedString(release.Object, "spec", "chart", "spec", "chart")
	chartVersion, _, _ := unstructured.NestedString(release.Object, "spec", "chart", "spec", "version")

	sourceKind, _, _ := unstructured.NestedString(release.Object, "spec", "chart", "spec", "sourceRef", "kind")
	sourceName, _, _ := unstructured.NestedString(release.Object, "spec", "chart", "spec", "sourceRef", "name")
	sourceNS, found, _ := unstructured.NestedString(release.Object, "spec", "chart", "spec", "sourceRef", "namespace")
	if !found {
		sourceNS = ns
	}

	repoDisplay := repoURL
	if repoDisplay == "" {
		repoDisplay = fmt.Sprintf("%s:%s/%s", sourceKind, sourceNS, sourceName)
	}

	// spec.chartRef (Flux HelmRelease v2's alternate chart-reference shape
	// — referencing an OCIRepository or HelmChart object directly instead
	// of the inline spec.chart.spec block, introduced specifically for
	// OCI-based charts). Only consulted when the inline shape didn't give
	// us a chart name — whichever shape is actually set wins.
	if chartName == "" {
		if refChart, refVersion, refRepo, refDisplay, ok := s.resolveChartRef(ctx, release, ns); ok {
			chartName = refChart
			chartVersion = refVersion
			repoURL = refRepo
			repoDisplay = refDisplay
		}
	}

	// spec.targetNamespace is the namespace Helm actually installs into —
	// it commonly differs from the HelmRelease CR's own metadata.namespace
	// (control objects in flux-system, workloads elsewhere). Flux's own
	// documented default when unset is the HelmRelease's own namespace.
	installNamespace, found, _ := unstructured.NestedString(release.Object, "spec", "targetNamespace")
	if !found || installNamespace == "" {
		installNamespace = ns
	}

	res := types.Resource{
		Name:           name,
		Namespace:      installNamespace,
		Type:           types.ResourceTypeFluxRelease,
		CurrentVersion: chartVersion,
		Repository:     repoDisplay,
		UpdateStatus:   types.UpdateStatusUnknown,
	}

	// spec.kubeConfig signals Flux's own hub-and-spoke multi-cluster
	// pattern: this HelmRelease is reconciled here but installs on a
	// remote cluster via a kubeconfig Secret/ConfigMap reference. We
	// deliberately do NOT dereference that Secret/ConfigMap's contents
	// (that would mean reading potentially sensitive remote-cluster
	// credentials) — we only surface that the release is remote, reusing
	// the same DestinationServer/DestinationNamespace fields ArgoCD's
	// spec.destination attribution already populates, so both GitOps
	// scanners report "where does this actually run" the same way.
	if ref := remoteKubeConfigRef(release); ref != "" {
		res.DestinationServer = fmt.Sprintf("remote (kubeconfig: %s)", ref)
		res.DestinationNamespace = installNamespace
	}

	// Resolve LatestVersion via direct index.yaml fetch — same path
	// `helm.go` uses, so output columns are identical across scanners.
	// No-op for GitRepository / oci:// / blank repoURL by design.
	ResolveLatestFromIndex(ctx, s.helmClient, s.logger, &res, repoURL, chartName, chartVersion)

	return res
}

// remoteKubeConfigRef returns a short, informative reference to the
// kubeconfig Secret or ConfigMap named in spec.kubeConfig, or "" when
// spec.kubeConfig is unset (the common case — the release targets the
// same cluster being scanned). Only the reference *name* is read, never
// the Secret/ConfigMap's actual contents.
func remoteKubeConfigRef(release unstructured.Unstructured) string {
	kubeConfig, found, _ := unstructured.NestedMap(release.Object, "spec", "kubeConfig")
	if !found || kubeConfig == nil {
		return ""
	}
	if secretRef, ok := kubeConfig["secretRef"].(map[string]interface{}); ok {
		if name, _ := secretRef["name"].(string); name != "" {
			return "secretRef:" + name
		}
	}
	if configMapRef, ok := kubeConfig["configMapRef"].(map[string]interface{}); ok {
		if name, _ := configMapRef["name"].(string); name != "" {
			return "configMapRef:" + name
		}
	}
	// spec.kubeConfig was present but neither ref shape matched — still a
	// remote release, just without a name we can surface.
	return "unspecified"
}

// resolveSourceRef resolves a HelmRelease's sourceRef to a HelmRepository URL.
func (s *FluxScanner) resolveSourceRef(ctx context.Context, release unstructured.Unstructured, repoMap map[string]string) string {
	sourceKind, _, _ := unstructured.NestedString(release.Object, "spec", "chart", "spec", "sourceRef", "kind")
	sourceName, _, _ := unstructured.NestedString(release.Object, "spec", "chart", "spec", "sourceRef", "name")
	sourceNS, found, _ := unstructured.NestedString(release.Object, "spec", "chart", "spec", "sourceRef", "namespace")
	if !found {
		sourceNS = release.GetNamespace()
	}

	key := fmt.Sprintf("%s/%s", sourceNS, sourceName)

	switch sourceKind {
	case "HelmRepository":
		if url, ok := repoMap[key]; ok {
			return url
		}
	case "GitRepository":
		// Fetch GitRepository to get URL
		repo, err := s.dynamicClient.Resource(gitRepositoryGVR).Namespace(sourceNS).Get(ctx, sourceName, metav1.GetOptions{})
		if err != nil {
			s.logger.V(1).Info("Failed to get GitRepository", "name", sourceName, "namespace", sourceNS, "error", err)
			return ""
		}
		url, _, _ := unstructured.NestedString(repo.Object, "spec", "url")
		return url
	case "OCIRepository":
		// Without this case the OCI-sourced HelmRelease fell through to
		// the "Kind:ns/name" placeholder display instead of a real
		// oci:// URL, so output correctly shows an OCI repository —
		// table output's transparency notes key off a Repository
		// literally starting with "oci://".
		return s.getOCIRepositoryURL(ctx, sourceNS, sourceName)
	}

	return ""
}

// getOCIRepositoryURL fetches the OCIRepository named `name` in
// `namespace` and returns its spec.url, or "" if the object can't be
// found or has no URL set. Shared between resolveSourceRef's
// OCIRepository case and resolveChartRef's HelmChart→OCIRepository
// sourceRef indirection.
func (s *FluxScanner) getOCIRepositoryURL(ctx context.Context, namespace, name string) string {
	obj, err := s.dynamicClient.Resource(ociRepositoryGVR).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		s.logger.V(1).Info("Failed to get OCIRepository", "name", name, "namespace", namespace, "error", err)
		return ""
	}
	url, _, _ := unstructured.NestedString(obj.Object, "spec", "url")
	return url
}

// resolveChartRef handles Flux HelmRelease v2's spec.chartRef — an
// alternate way to reference a chart directly via an OCIRepository or
// HelmChart object, instead of the inline spec.chart.spec block. It's
// only consulted by processHelmRelease when that inline block didn't
// yield a chart name.
//
// Returns ok=false when spec.chartRef is absent or its referenced object
// can't be resolved — callers keep today's existing blank-field behavior
// in that case, they don't error out.
func (s *FluxScanner) resolveChartRef(ctx context.Context, release unstructured.Unstructured, defaultNS string) (chartName, chartVersion, repoURL, repoDisplay string, ok bool) {
	kind, _, _ := unstructured.NestedString(release.Object, "spec", "chartRef", "kind")
	refName, _, _ := unstructured.NestedString(release.Object, "spec", "chartRef", "name")
	refNS, found, _ := unstructured.NestedString(release.Object, "spec", "chartRef", "namespace")
	if !found || refNS == "" {
		refNS = defaultNS
	}
	if kind == "" || refName == "" {
		return "", "", "", "", false
	}

	switch kind {
	case "HelmChart":
		obj, err := s.dynamicClient.Resource(helmChartGVR).Namespace(refNS).Get(ctx, refName, metav1.GetOptions{})
		if err != nil {
			s.logger.V(1).Info("Failed to get HelmChart referenced by spec.chartRef",
				"name", refName, "namespace", refNS, "error", err)
			return "", "", "", "", false
		}
		chartName, _, _ = unstructured.NestedString(obj.Object, "spec", "chart")
		chartVersion, _, _ = unstructured.NestedString(obj.Object, "spec", "version")

		srcKind, _, _ := unstructured.NestedString(obj.Object, "spec", "sourceRef", "kind")
		srcName, _, _ := unstructured.NestedString(obj.Object, "spec", "sourceRef", "name")
		srcNS, srcFound, _ := unstructured.NestedString(obj.Object, "spec", "sourceRef", "namespace")
		if !srcFound || srcNS == "" {
			srcNS = refNS
		}
		switch srcKind {
		case "HelmRepository":
			if repo, err := s.dynamicClient.Resource(helmRepositoryGVR).Namespace(srcNS).Get(ctx, srcName, metav1.GetOptions{}); err == nil {
				repoURL, _, _ = unstructured.NestedString(repo.Object, "spec", "url")
			}
		case "OCIRepository":
			repoURL = s.getOCIRepositoryURL(ctx, srcNS, srcName)
		}
		repoDisplay = repoURL
		if repoDisplay == "" {
			repoDisplay = fmt.Sprintf("HelmChart:%s/%s", refNS, refName)
		}
		return chartName, chartVersion, repoURL, repoDisplay, true

	case "OCIRepository":
		obj, err := s.dynamicClient.Resource(ociRepositoryGVR).Namespace(refNS).Get(ctx, refName, metav1.GetOptions{})
		if err != nil {
			s.logger.V(1).Info("Failed to get OCIRepository referenced by spec.chartRef",
				"name", refName, "namespace", refNS, "error", err)
			return "", "", "", "", false
		}
		url, _, _ := unstructured.NestedString(obj.Object, "spec", "url")
		if url == "" {
			return "", "", "", "", false
		}
		tag, _, _ := unstructured.NestedString(obj.Object, "spec", "ref", "tag")
		version := tag
		if version == "" {
			// Only a semver range is set, not a pinned tag — surface it
			// honestly as the range string rather than fabricating a
			// fake pinned version.
			version, _, _ = unstructured.NestedString(obj.Object, "spec", "ref", "semver")
		}
		// OCIRepository has no chart-name field of its own (the chart
		// identity is implicit in spec.url) — use the chartRef's own
		// referenced-object name as a stand-in identifier, same spirit
		// as this file's existing "Kind:ns/name" placeholder pattern.
		return refName, version, url, url, true
	}

	return "", "", "", "", false
}
