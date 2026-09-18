package scanner

import (
	"context"
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// buildMultiSourceApp constructs a minimal ArgoCD Application with
// spec.sources (plural) instead of spec.source, for testing the D2
// multi-source fan-out without a live cluster or a real ArgoCD sync — the
// scanner only ever reads `spec`.
func buildMultiSourceApp(name, namespace string, sources []interface{}, destination map[string]interface{}) unstructured.Unstructured {
	spec := map[string]interface{}{
		"sources": sources,
	}
	if destination != nil {
		spec["destination"] = destination
	}
	return unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "argoproj.io/v1alpha1",
			"kind":       "Application",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": namespace,
			},
			"spec": spec,
		},
	}
}

func TestProcessApplication_SingleSourceUnaffectedByD2(t *testing.T) {
	// Regression guard: D2 must not change the existing singular
	// spec.source path at all. buildApp (from argocd_destination_test.go)
	// always sets spec.source, never spec.sources.
	s := NewArgoCDScanner(nil, nil, nil, "api", logr.Discard())
	app := buildApp("single-source-app", "argocd", nil)

	resources, err := s.processApplication(context.Background(), app, ScanOptions{}, nil)
	assert.NoError(t, err)
	if assert.Len(t, resources, 1) {
		assert.Equal(t, "single-source-app", resources[0].Name)
		assert.NotContains(t, resources[0].Name, "/")
		assert.Equal(t, "1.0.0", resources[0].CurrentVersion)
	}
}

func TestProcessApplication_MultiSource_FansOutEachChart(t *testing.T) {
	s := NewArgoCDScanner(nil, nil, nil, "api", logr.Discard())

	sources := []interface{}{
		map[string]interface{}{
			"chart":          "ingress-nginx",
			"targetRevision": "4.10.0",
			"repoURL":        "https://kubernetes.github.io/ingress-nginx",
		},
		map[string]interface{}{
			"chart":          "cert-manager",
			"targetRevision": "1.14.0",
			"repoURL":        "https://charts.jetstack.io",
		},
	}
	app := buildMultiSourceApp("multi-app", "argocd", sources, map[string]interface{}{
		"server":    "https://spoke-c.example.com:6443",
		"namespace": "prod",
	})

	resources, err := s.processApplication(context.Background(), app, ScanOptions{}, nil)
	assert.NoError(t, err)
	if assert.Len(t, resources, 2) {
		assert.Equal(t, "multi-app/ingress-nginx", resources[0].Name)
		assert.Equal(t, "4.10.0", resources[0].CurrentVersion)
		assert.Equal(t, "https://kubernetes.github.io/ingress-nginx", resources[0].Repository)
		assert.Equal(t, "https://spoke-c.example.com:6443", resources[0].DestinationServer)
		assert.Equal(t, "prod", resources[0].DestinationNamespace)

		assert.Equal(t, "multi-app/cert-manager", resources[1].Name)
		assert.Equal(t, "1.14.0", resources[1].CurrentVersion)
		assert.Equal(t, "https://charts.jetstack.io", resources[1].Repository)
		assert.Equal(t, "https://spoke-c.example.com:6443", resources[1].DestinationServer)
		assert.Equal(t, "prod", resources[1].DestinationNamespace)
	}
}

func TestProcessApplication_MultiSource_SkipsChartlessEntries(t *testing.T) {
	s := NewArgoCDScanner(nil, nil, nil, "api", logr.Discard())

	sources := []interface{}{
		// Values-only companion source: no chart field at all.
		map[string]interface{}{
			"repoURL":        "https://git.example.com/values-repo.git",
			"targetRevision": "main",
			"ref":            "values",
		},
		map[string]interface{}{
			"chart":          "argo-cd",
			"targetRevision": "7.1.0",
			"repoURL":        "https://argoproj.github.io/argo-helm",
		},
	}
	app := buildMultiSourceApp("mixed-app", "argocd", sources, nil)

	resources, err := s.processApplication(context.Background(), app, ScanOptions{}, nil)
	assert.NoError(t, err)
	if assert.Len(t, resources, 1) {
		assert.Equal(t, "mixed-app/argo-cd", resources[0].Name)
		assert.Equal(t, "7.1.0", resources[0].CurrentVersion)
	}
}

func TestProcessApplication_MultiSource_NoChartBearingSourcesReturnsEmpty(t *testing.T) {
	s := NewArgoCDScanner(nil, nil, nil, "api", logr.Discard())

	sources := []interface{}{
		map[string]interface{}{
			"repoURL":        "https://git.example.com/repo-a.git",
			"targetRevision": "main",
		},
		map[string]interface{}{
			"repoURL":        "https://git.example.com/repo-b.git",
			"targetRevision": "main",
		},
	}
	app := buildMultiSourceApp("no-chart-app", "argocd", sources, nil)

	resources, err := s.processApplication(context.Background(), app, ScanOptions{}, nil)
	assert.NoError(t, err)
	assert.Empty(t, resources)
}
