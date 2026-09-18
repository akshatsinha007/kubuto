package scanner

import (
	"context"
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// buildApp constructs a minimal ArgoCD Application object with an
// optional spec.destination block, for testing destinationFields and its
// propagation into scan results without a live cluster.
func buildApp(name, namespace string, destination map[string]interface{}) unstructured.Unstructured {
	spec := map[string]interface{}{
		"source": map[string]interface{}{
			"chart":          "test-chart",
			"targetRevision": "1.0.0",
			"repoURL":        "",
		},
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

func TestDestinationFields(t *testing.T) {
	t.Run("reads server and namespace", func(t *testing.T) {
		app := buildApp("app1", "argocd", map[string]interface{}{
			"server":    "https://spoke-a.example.com:6443",
			"namespace": "workloads",
		})
		server, ns := destinationFields(app)
		assert.Equal(t, "https://spoke-a.example.com:6443", server)
		assert.Equal(t, "workloads", ns)
	})

	t.Run("falls back to name when server is blank", func(t *testing.T) {
		app := buildApp("app2", "argocd", map[string]interface{}{
			"name":      "spoke-b",
			"namespace": "workloads",
		})
		server, ns := destinationFields(app)
		assert.Equal(t, "spoke-b", server)
		assert.Equal(t, "workloads", ns)
	})

	t.Run("prefers server over name when both set", func(t *testing.T) {
		app := buildApp("app3", "argocd", map[string]interface{}{
			"server": "https://spoke-a.example.com:6443",
			"name":   "spoke-a-alias",
		})
		server, _ := destinationFields(app)
		assert.Equal(t, "https://spoke-a.example.com:6443", server)
	})

	t.Run("returns empty strings when spec.destination is absent", func(t *testing.T) {
		app := buildApp("app4", "argocd", nil)
		server, ns := destinationFields(app)
		assert.Empty(t, server)
		assert.Empty(t, ns)
	})
}

func TestProcessApplication_PropagatesDestination(t *testing.T) {
	s := NewArgoCDScanner(nil, nil, nil, "api", logr.Discard())

	app := buildApp("my-app", "argocd", map[string]interface{}{
		"server":    "https://spoke-a.example.com:6443",
		"namespace": "workloads",
	})

	resources, err := s.processApplication(context.Background(), app, ScanOptions{}, nil)
	assert.NoError(t, err)
	if assert.Len(t, resources, 1) {
		assert.Equal(t, "https://spoke-a.example.com:6443", resources[0].DestinationServer)
		assert.Equal(t, "workloads", resources[0].DestinationNamespace)
	}
}

func TestProcessApplication_NoDestinationSet(t *testing.T) {
	s := NewArgoCDScanner(nil, nil, nil, "api", logr.Discard())

	app := buildApp("my-app", "argocd", nil)

	resources, err := s.processApplication(context.Background(), app, ScanOptions{}, nil)
	assert.NoError(t, err)
	if assert.Len(t, resources, 1) {
		assert.Empty(t, resources[0].DestinationServer)
		assert.Empty(t, resources[0].DestinationNamespace)
	}
}

func TestProcessGitSource_PropagatesDestinationToUmbrellaAndDeps(t *testing.T) {
	s := NewArgoCDScanner(nil, nil, nil, "api", logr.Discard())
	// gitClient left nil deliberately: the umbrella-only short-circuit
	// path (argocd.go's defensive nil-gitClient guard) still needs
	// destination fields to reach the returned resource.
	source := map[string]interface{}{
		"repoURL":        "https://git.example.com/repo.git",
		"path":           "charts/app",
		"targetRevision": "main",
	}
	app := buildApp("umbrella-app", "argocd", map[string]interface{}{
		"server":    "https://spoke-b.example.com:6443",
		"namespace": "prod",
	})

	resources, err := s.processGitSource(context.Background(), app, source, ScanOptions{}, nil, appMeta{destServer: "https://spoke-b.example.com:6443", destNamespace: "prod"})
	assert.NoError(t, err)
	if assert.Len(t, resources, 1) {
		assert.Equal(t, "https://spoke-b.example.com:6443", resources[0].DestinationServer)
		assert.Equal(t, "prod", resources[0].DestinationNamespace)
	}
}
