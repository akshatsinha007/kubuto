package scanner

import (
	"context"
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// buildHelmRelease constructs a minimal Flux HelmRelease object with
// optional spec.targetNamespace and spec.kubeConfig blocks, for testing
// processHelmRelease's location attribution without a live cluster.
func buildHelmRelease(name, namespace, targetNamespace string, kubeConfig map[string]interface{}) unstructured.Unstructured {
	spec := map[string]interface{}{
		"chart": map[string]interface{}{
			"spec": map[string]interface{}{
				"chart":   "test-chart",
				"version": "1.0.0",
			},
		},
	}
	if targetNamespace != "" {
		spec["targetNamespace"] = targetNamespace
	}
	if kubeConfig != nil {
		spec["kubeConfig"] = kubeConfig
	}
	return unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "helm.toolkit.fluxcd.io/v2",
			"kind":       "HelmRelease",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": namespace,
			},
			"spec": spec,
		},
	}
}

func TestRemoteKubeConfigRef(t *testing.T) {
	t.Run("returns empty string when spec.kubeConfig is absent", func(t *testing.T) {
		rel := buildHelmRelease("app1", "flux-system", "", nil)
		assert.Empty(t, remoteKubeConfigRef(rel))
	})

	t.Run("reads secretRef name", func(t *testing.T) {
		rel := buildHelmRelease("app2", "flux-system", "", map[string]interface{}{
			"secretRef": map[string]interface{}{"name": "spoke-a-kubeconfig"},
		})
		assert.Equal(t, "secretRef:spoke-a-kubeconfig", remoteKubeConfigRef(rel))
	})

	t.Run("reads configMapRef name", func(t *testing.T) {
		rel := buildHelmRelease("app3", "flux-system", "", map[string]interface{}{
			"configMapRef": map[string]interface{}{"name": "spoke-b-kubeconfig"},
		})
		assert.Equal(t, "configMapRef:spoke-b-kubeconfig", remoteKubeConfigRef(rel))
	})

	t.Run("returns a sentinel when kubeConfig is present but unrecognized", func(t *testing.T) {
		rel := buildHelmRelease("app4", "flux-system", "", map[string]interface{}{
			"somethingElse": "x",
		})
		assert.Equal(t, "unspecified", remoteKubeConfigRef(rel))
	})
}

func TestProcessHelmRelease_TargetNamespace(t *testing.T) {
	s := NewFluxScanner(nil, nil, nil, logr.Discard())

	t.Run("uses spec.targetNamespace when set and differs from CR namespace", func(t *testing.T) {
		rel := buildHelmRelease("my-app", "flux-system", "production", nil)
		res := s.processHelmRelease(context.Background(), rel, ScanOptions{}, "")
		assert.Equal(t, "production", res.Namespace)
	})

	t.Run("falls back to the CR's own namespace when targetNamespace is unset", func(t *testing.T) {
		rel := buildHelmRelease("my-app", "flux-system", "", nil)
		res := s.processHelmRelease(context.Background(), rel, ScanOptions{}, "")
		assert.Equal(t, "flux-system", res.Namespace)
	})
}

func TestProcessHelmRelease_RemoteKubeConfig(t *testing.T) {
	s := NewFluxScanner(nil, nil, nil, logr.Discard())

	t.Run("populates DestinationServer/DestinationNamespace when kubeConfig is set", func(t *testing.T) {
		rel := buildHelmRelease("my-app", "flux-system", "workloads", map[string]interface{}{
			"secretRef": map[string]interface{}{"name": "spoke-a-kubeconfig"},
		})
		res := s.processHelmRelease(context.Background(), rel, ScanOptions{}, "")
		assert.Equal(t, "remote (kubeconfig: secretRef:spoke-a-kubeconfig)", res.DestinationServer)
		assert.Equal(t, "workloads", res.DestinationNamespace)
	})

	t.Run("leaves DestinationServer/DestinationNamespace empty for a local release", func(t *testing.T) {
		rel := buildHelmRelease("my-app", "flux-system", "", nil)
		res := s.processHelmRelease(context.Background(), rel, ScanOptions{}, "")
		assert.Empty(t, res.DestinationServer)
		assert.Empty(t, res.DestinationNamespace)
	})
}
