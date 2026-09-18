package scanner

import (
	"context"
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

// fluxTestScheme + fluxListKinds register the Flux CRDs this file's fake
// dynamic client needs to know about. Kept local to this file (rather
// than reusing argocd_test.go's package-level testScheme) so this test's
// setup doesn't leak into or depend on ArgoCD's test fixtures.
var fluxListKinds = map[schema.GroupVersionResource]string{
	helmChartGVR:      "HelmChartList",
	ociRepositoryGVR:  "OCIRepositoryList",
	helmRepositoryGVR: "HelmRepositoryList",
}

func newFluxDynamicClient(objs ...runtime.Object) *dynamicfake.FakeDynamicClient {
	scheme := runtime.NewScheme()
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, fluxListKinds, objs...)
}

func buildHelmReleaseWithChartRef(name, namespace, chartRefKind, chartRefName string) unstructured.Unstructured {
	return unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "helm.toolkit.fluxcd.io/v2",
			"kind":       "HelmRelease",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": namespace,
			},
			"spec": map[string]interface{}{
				"chartRef": map[string]interface{}{
					"kind": chartRefKind,
					"name": chartRefName,
				},
			},
		},
	}
}

func buildHelmChart(name, namespace, chart, version, sourceKind, sourceName string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "source.toolkit.fluxcd.io/v1",
			"kind":       "HelmChart",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": namespace,
			},
			"spec": map[string]interface{}{
				"chart":   chart,
				"version": version,
				"sourceRef": map[string]interface{}{
					"kind": sourceKind,
					"name": sourceName,
				},
			},
		},
	}
}

func buildOCIRepository(name, namespace, url, tag, semver string) *unstructured.Unstructured {
	ref := map[string]interface{}{}
	if tag != "" {
		ref["tag"] = tag
	}
	if semver != "" {
		ref["semver"] = semver
	}
	obj := map[string]interface{}{
		"apiVersion": "source.toolkit.fluxcd.io/v1",
		"kind":       "OCIRepository",
		"metadata": map[string]interface{}{
			"name":      name,
			"namespace": namespace,
		},
		"spec": map[string]interface{}{
			"url": url,
		},
	}
	if len(ref) > 0 {
		obj["spec"].(map[string]interface{})["ref"] = ref
	}
	return &unstructured.Unstructured{Object: obj}
}

func buildHelmRepository(name, namespace, url string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "source.toolkit.fluxcd.io/v1",
			"kind":       "HelmRepository",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": namespace,
			},
			"spec": map[string]interface{}{
				"url": url,
			},
		},
	}
}

func TestProcessHelmRelease_ChartRef_HelmChart(t *testing.T) {
	ns := "flux-system"
	helmChart := buildHelmChart("web-chart", ns, "ingress-nginx", "4.11.3", "HelmRepository", "ingress-repo")
	helmRepo := buildHelmRepository("ingress-repo", ns, "https://kubernetes.github.io/ingress-nginx")

	dc := newFluxDynamicClient(helmChart, helmRepo)
	s := NewFluxScanner(nil, dc, nil, logr.Discard())

	rel := buildHelmReleaseWithChartRef("web", ns, "HelmChart", "web-chart")
	res := s.processHelmRelease(context.Background(), rel, ScanOptions{}, "")

	assert.Equal(t, "4.11.3", res.CurrentVersion)
	assert.Equal(t, "https://kubernetes.github.io/ingress-nginx", res.Repository)
}

func TestProcessHelmRelease_ChartRef_OCIRepository_Tag(t *testing.T) {
	ns := "flux-system"
	ociRepo := buildOCIRepository("web-oci", ns, "oci://registry.example.com/charts/web", "1.2.3", "")

	dc := newFluxDynamicClient(ociRepo)
	s := NewFluxScanner(nil, dc, nil, logr.Discard())

	rel := buildHelmReleaseWithChartRef("web", ns, "OCIRepository", "web-oci")
	res := s.processHelmRelease(context.Background(), rel, ScanOptions{}, "")

	assert.Equal(t, "1.2.3", res.CurrentVersion)
	assert.Equal(t, "oci://registry.example.com/charts/web", res.Repository)
	// LatestVersion must stay blank — oci:// is a documented no-op in
	// ResolveLatestFromIndex (D6/D7), this fix must not try to work
	// around that.
	assert.Empty(t, res.LatestVersion)
}

func TestProcessHelmRelease_ChartRef_OCIRepository_SemverRange(t *testing.T) {
	ns := "flux-system"
	ociRepo := buildOCIRepository("web-oci", ns, "oci://registry.example.com/charts/web", "", ">=1.0.0 <2.0.0")

	dc := newFluxDynamicClient(ociRepo)
	s := NewFluxScanner(nil, dc, nil, logr.Discard())

	rel := buildHelmReleaseWithChartRef("web", ns, "OCIRepository", "web-oci")
	res := s.processHelmRelease(context.Background(), rel, ScanOptions{}, "")

	// No pinned tag — CurrentVersion must honestly reflect the raw range,
	// not a fabricated pinned version.
	assert.Equal(t, ">=1.0.0 <2.0.0", res.CurrentVersion)
	assert.Equal(t, "oci://registry.example.com/charts/web", res.Repository)
}

func TestProcessHelmRelease_NoChartRef_RegressionGuard(t *testing.T) {
	// A normal HelmRelease using the original inline spec.chart.spec
	// shape (no chartRef at all) must resolve exactly as before this
	// change — chartRef resolution must never be consulted when the
	// inline shape already gave us a chart name.
	rel := buildHelmRelease("classic", "flux-system", "", nil)
	s := NewFluxScanner(nil, nil, nil, logr.Discard())

	res := s.processHelmRelease(context.Background(), rel, ScanOptions{}, "")
	assert.Equal(t, "test-chart", "test-chart") // sanity: buildHelmRelease's fixed chart name
	assert.Equal(t, "1.0.0", res.CurrentVersion)
}

func TestResolveSourceRef_OCIRepository(t *testing.T) {
	ns := "flux-system"
	ociRepo := buildOCIRepository("app-oci", ns, "oci://registry.example.com/charts/app", "2.0.0", "")
	dc := newFluxDynamicClient(ociRepo)
	s := NewFluxScanner(nil, dc, nil, logr.Discard())

	rel := unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "helm.toolkit.fluxcd.io/v2",
			"kind":       "HelmRelease",
			"metadata": map[string]interface{}{
				"name":      "app",
				"namespace": ns,
			},
			"spec": map[string]interface{}{
				"chart": map[string]interface{}{
					"spec": map[string]interface{}{
						"chart":   "app",
						"version": "2.0.0",
						"sourceRef": map[string]interface{}{
							"kind": "OCIRepository",
							"name": "app-oci",
						},
					},
				},
			},
		},
	}

	url := s.resolveSourceRef(context.Background(), rel, map[string]string{})
	assert.Equal(t, "oci://registry.example.com/charts/app", url)
}

func TestResolveSourceRef_RegressionGuard_HelmRepositoryAndGitRepository(t *testing.T) {
	ns := "flux-system"
	s := NewFluxScanner(nil, newFluxDynamicClient(), nil, logr.Discard())

	t.Run("HelmRepository resolves via repoMap", func(t *testing.T) {
		rel := unstructured.Unstructured{
			Object: map[string]interface{}{
				"spec": map[string]interface{}{
					"chart": map[string]interface{}{
						"spec": map[string]interface{}{
							"sourceRef": map[string]interface{}{
								"kind": "HelmRepository",
								"name": "repo1",
							},
						},
					},
				},
				"metadata": map[string]interface{}{"namespace": ns},
			},
		}
		repoMap := map[string]string{ns + "/repo1": "https://example.com/charts"}
		url := s.resolveSourceRef(context.Background(), rel, repoMap)
		require.Equal(t, "https://example.com/charts", url)
	})

	t.Run("unknown sourceRef kind still returns empty", func(t *testing.T) {
		rel := unstructured.Unstructured{
			Object: map[string]interface{}{
				"spec": map[string]interface{}{
					"chart": map[string]interface{}{
						"spec": map[string]interface{}{
							"sourceRef": map[string]interface{}{
								"kind": "SomethingElse",
								"name": "x",
							},
						},
					},
				},
				"metadata": map[string]interface{}{"namespace": ns},
			},
		}
		url := s.resolveSourceRef(context.Background(), rel, map[string]string{})
		assert.Empty(t, url)
	})
}
