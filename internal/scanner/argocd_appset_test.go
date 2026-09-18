package scanner

import (
	"context"
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// buildAppWithOwner constructs a minimal ArgoCD Application object with an
// optional ownerReferences entry, for testing applicationSetOwner and its
// propagation into scan results without a live cluster. `source` lets each
// test exercise a different source shape (single chart, git umbrella, or
// multi-source) through the same owner-reference plumbing.
func buildAppWithOwner(name, namespace string, ownerKind, ownerName string, spec map[string]interface{}) unstructured.Unstructured {
	obj := map[string]interface{}{
		"apiVersion": "argoproj.io/v1alpha1",
		"kind":       "Application",
		"metadata": map[string]interface{}{
			"name":      name,
			"namespace": namespace,
		},
		"spec": spec,
	}
	if ownerKind != "" {
		obj["metadata"].(map[string]interface{})["ownerReferences"] = []interface{}{
			map[string]interface{}{
				"apiVersion":         "argoproj.io/v1alpha1",
				"kind":               ownerKind,
				"name":               ownerName,
				"uid":                "11111111-1111-1111-1111-111111111111",
				"controller":         true,
				"blockOwnerDeletion": true,
			},
		}
	}
	return unstructured.Unstructured{Object: obj}
}

func directHelmSpec() map[string]interface{} {
	return map[string]interface{}{
		"source": map[string]interface{}{
			"chart":          "test-chart",
			"targetRevision": "1.0.0",
			"repoURL":        "https://charts.example.com",
		},
	}
}

func multiSourceSpec() map[string]interface{} {
	return map[string]interface{}{
		"sources": []interface{}{
			map[string]interface{}{
				"chart":          "chart-a",
				"targetRevision": "1.0.0",
				"repoURL":        "https://charts.example.com/a",
			},
			map[string]interface{}{
				"chart":          "chart-b",
				"targetRevision": "2.0.0",
				"repoURL":        "https://charts.example.com/b",
			},
		},
	}
}

func TestApplicationSetOwner(t *testing.T) {
	t.Run("finds an ApplicationSet owner reference", func(t *testing.T) {
		app := buildAppWithOwner("generated-app", "argocd", "ApplicationSet", "my-appset", directHelmSpec())
		assert.Equal(t, "my-appset", applicationSetOwner(app))
	})

	t.Run("returns empty for a standalone application with no owner", func(t *testing.T) {
		app := buildAppWithOwner("standalone-app", "argocd", "", "", directHelmSpec())
		assert.Empty(t, applicationSetOwner(app))
	})

	t.Run("returns empty when owned by something other than an ApplicationSet", func(t *testing.T) {
		app := buildAppWithOwner("child-app", "argocd", "Application", "parent-app", directHelmSpec())
		assert.Empty(t, applicationSetOwner(app))
	})
}

func TestProcessApplication_PropagatesGeneratedBy_DirectHelmSource(t *testing.T) {
	s := NewArgoCDScanner(nil, nil, nil, "api", logr.Discard())

	app := buildAppWithOwner("generated-app", "argocd", "ApplicationSet", "my-appset", directHelmSpec())
	resources, err := s.processApplication(context.Background(), app, ScanOptions{}, nil)
	assert.NoError(t, err)
	if assert.Len(t, resources, 1) {
		assert.Equal(t, "my-appset", resources[0].GeneratedBy)
	}
}

func TestProcessApplication_NoOwner_GeneratedByEmpty(t *testing.T) {
	s := NewArgoCDScanner(nil, nil, nil, "api", logr.Discard())

	app := buildAppWithOwner("standalone-app", "argocd", "", "", directHelmSpec())
	resources, err := s.processApplication(context.Background(), app, ScanOptions{}, nil)
	assert.NoError(t, err)
	if assert.Len(t, resources, 1) {
		assert.Empty(t, resources[0].GeneratedBy)
	}
}

func TestProcessApplication_PropagatesGeneratedBy_MultiSourceFanOut(t *testing.T) {
	s := NewArgoCDScanner(nil, nil, nil, "api", logr.Discard())

	app := buildAppWithOwner("fleet-app", "argocd", "ApplicationSet", "fleet-appset", multiSourceSpec())
	resources, err := s.processApplication(context.Background(), app, ScanOptions{}, nil)
	assert.NoError(t, err)
	if assert.Len(t, resources, 2) {
		for _, r := range resources {
			assert.Equal(t, "fleet-appset", r.GeneratedBy)
		}
		assert.Equal(t, "fleet-app/chart-a", resources[0].Name)
		assert.Equal(t, "fleet-app/chart-b", resources[1].Name)
	}
}
