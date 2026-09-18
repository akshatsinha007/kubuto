package scanner

import (
	"context"
	"fmt"
	"testing"

	"github.com/akshatsinha007/kubuto/pkg/types"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// mockRegistryClient is a mock implementation of the RegistryClient interface.
type mockRegistryClient struct {
	tags map[string][]string
}

func (m *mockRegistryClient) GetTags(ctx context.Context, image string) ([]string, error) {
	if tags, ok := m.tags[image]; ok {
		return tags, nil
	}
	return nil, fmt.Errorf("image not found")
}

func TestImageScanner_Scan(t *testing.T) {
	// Setup
	mockRegistry := &mockRegistryClient{
		tags: map[string][]string{
			"nginx":                           {"1.20.0", "1.21.0"},
			"quay.io/prometheus/alertmanager": {"v0.23.0", "v0.24.0"},
		},
	}

	fakeKubeClient := fake.NewSimpleClientset(
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "dep1", Namespace: "default"},
			Spec: appsv1.DeploymentSpec{
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{Image: "nginx:1.20.0"},
							{Image: "my-internal-image:1.0.0"}, // No tags in mock registry
						},
					},
				},
			},
		},
		&appsv1.DaemonSet{
			ObjectMeta: metav1.ObjectMeta{Name: "ds1", Namespace: "kube-system"},
			Spec: appsv1.DaemonSetSpec{
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{Image: "quay.io/prometheus/alertmanager:v0.23.0"},
						},
					},
				},
			},
		},
	)

	scanner := NewImageScanner(fakeKubeClient, mockRegistry, logr.Discard())

	// Execute
	resources, err := scanner.Scan(context.Background(), ScanOptions{Namespaces: []string{"default", "kube-system"}})
	require.NoError(t, err)

	// Assert
	assert.Len(t, resources, 2)

	nginxResource := findResource(resources, "nginx")
	require.NotNil(t, nginxResource)
	assert.Equal(t, "1.21.0", nginxResource.LatestVersion)
	assert.Equal(t, types.UpdateStatusMinor, nginxResource.UpdateStatus)

	alertmanagerResource := findResource(resources, "quay.io/prometheus/alertmanager")
	require.NotNil(t, alertmanagerResource)
	assert.Equal(t, "0.24.0", alertmanagerResource.LatestVersion)
	assert.Equal(t, types.UpdateStatusMinor, alertmanagerResource.UpdateStatus)

	t.Run("Image with Digest", func(t *testing.T) {
		mockRegistry := &mockRegistryClient{
			tags: map[string][]string{
				"ghcr.io/opencost/opencost": {"1.117.3", "1.118.0"},
			},
		}

		fakeKubeClient := fake.NewSimpleClientset(
			&appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{Name: "dep-digest", Namespace: "default"},
				Spec: appsv1.DeploymentSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{Image: "ghcr.io/opencost/opencost@sha256:09fae89c4544ec2a891e2902e88ce3137d7032c6edbe0fd97441aaee7bdbea54"},
							},
						},
					},
				},
			},
		)

		scanner := NewImageScanner(fakeKubeClient, mockRegistry, logr.Discard())
		resources, err := scanner.Scan(context.Background(), ScanOptions{Namespaces: []string{"default"}})
		require.NoError(t, err)

		assert.Len(t, resources, 1)
		digestResource := findResource(resources, "ghcr.io/opencost/opencost")
		require.NotNil(t, digestResource)
		assert.Equal(t, "1.118.0", digestResource.LatestVersion)
		assert.Equal(t, types.UpdateStatusDigestUpdateAvailable, digestResource.UpdateStatus)
	})
}

func findResource(resources []types.Resource, name string) *types.Resource {
	for i := range resources {
		if resources[i].Name == name {
			return &resources[i]
		}
	}
	return nil
}
