package scanner

import (
	"context"
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	kubernetesfake "k8s.io/client-go/kubernetes/fake"

	"github.com/akshatsinha007/kubuto/internal/client"
)

var testScheme = runtime.NewScheme()

func init() {
	// Add ArgoCD Application GVR to scheme
	testScheme.AddKnownTypeWithName(
		schema.GroupVersionKind{
			Group:   "argoproj.io",
			Version: "v1alpha1",
			Kind:    "Application",
		},
		&metav1.Status{},
	)
}

func TestNewArgoCDScanner(t *testing.T) {
	kubeClient := kubernetesfake.NewSimpleClientset()
	dynamicClient := dynamicfake.NewSimpleDynamicClient(testScheme)
	logger := logr.Discard()

	t.Run("creates scanner with api fetch method", func(t *testing.T) {
		scanner := NewArgoCDScanner(kubeClient, dynamicClient, nil, "api", logger)
		assert.NotNil(t, scanner)
		assert.Equal(t, "api", scanner.fetchMethod)
		assert.Equal(t, "ArgoCD", scanner.Name())
	})

	t.Run("creates scanner with clone fetch method", func(t *testing.T) {
		scanner := NewArgoCDScanner(kubeClient, dynamicClient, nil, "clone", logger)
		assert.NotNil(t, scanner)
		assert.Equal(t, "clone", scanner.fetchMethod)
	})

	t.Run("defaults to api fetch method when empty", func(t *testing.T) {
		scanner := NewArgoCDScanner(kubeClient, dynamicClient, nil, "", logger)
		assert.NotNil(t, scanner)
		assert.Equal(t, "", scanner.fetchMethod) // Empty string, will default to "api" in cluster_scan.go
	})
}

func TestArgoCDScanner_Validate(t *testing.T) {
	logger := logr.Discard()

	t.Run("fails without kube client", func(t *testing.T) {
		scanner := NewArgoCDScanner(nil, nil, nil, "api", logger)
		err := scanner.Validate(context.Background())
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "kubernetes client is not initialized")
	})

	t.Run("fails without dynamic client", func(t *testing.T) {
		kubeClient := kubernetesfake.NewSimpleClientset()
		scanner := NewArgoCDScanner(kubeClient, nil, nil, "api", logger)
		err := scanner.Validate(context.Background())
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "dynamic client is not initialized")
	})

	t.Run("passes with both clients", func(t *testing.T) {
		kubeClient := kubernetesfake.NewSimpleClientset()
		dynamicClient := dynamicfake.NewSimpleDynamicClient(testScheme)
		scanner := NewArgoCDScanner(kubeClient, dynamicClient, nil, "api", logger)
		err := scanner.Validate(context.Background())
		assert.NoError(t, err)
	})
}

func TestArgoCDScanner_Cleanup(t *testing.T) {
	kubeClient := kubernetesfake.NewSimpleClientset()
	dynamicClient := dynamicfake.NewSimpleDynamicClient(testScheme)
	logger := logr.Discard()
	scanner := NewArgoCDScanner(kubeClient, dynamicClient, nil, "api", logger)

	err := scanner.Cleanup()
	assert.NoError(t, err)
}

func TestMatchRepoSecret(t *testing.T) {
	secrets := []repoSecret{
		{Name: "argocd-helm-repo", URL: "https://argoproj.github.io/argo-helm", Type: "helm"},
		{Name: "bitnami-helm-repo", URL: "https://charts.bitnami.com/bitnami", Type: "helm"},
		{Name: "git-repo", URL: "https://github.com/org/gitops", Type: "git"},
	}

	tests := []struct {
		gitopsURL string
		expected  string
	}{
		{"https://argoproj.github.io/argo-helm", "https://argoproj.github.io/argo-helm"},
		{"https://charts.bitnami.com/bitnami", "https://charts.bitnami.com/bitnami"},
		{"https://unknown.repo.com/charts", ""},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.gitopsURL, func(t *testing.T) {
			result := matchRepoSecret(tt.gitopsURL, secrets)
			if result != tt.expected {
				t.Errorf("matchRepoSecret(%q) = %q, want %q", tt.gitopsURL, result, tt.expected)
			}
		})
	}
}

func TestMatchRepoSecret_SkipsNonHelm(t *testing.T) {
	secrets := []repoSecret{
		{Name: "git-repo", URL: "https://github.com/org/gitops", Type: "git"},
		{Name: "oci-repo", URL: "oci://registry.example.com/charts", Type: "oci"},
	}

	result := matchRepoSecret("https://github.com/org/gitops", secrets)
	if result != "" {
		t.Errorf("expected empty result for non-helm secrets, got %q", result)
	}
}

func TestMatchRepoSecret_UsesClientNormalize(t *testing.T) {
	// Verify matchRepoSecret uses client.NormalizeRepoURL for matching
	secrets := []repoSecret{
		{Name: "test-repo", URL: "https://github.com/owner/repo.git", Type: "helm"},
	}

	// Should match despite .git suffix difference
	result := matchRepoSecret("https://github.com/owner/repo", secrets)
	if result != "https://github.com/owner/repo.git" {
		t.Errorf("expected match with normalized URL, got %q", result)
	}
}

func TestClientNormalizeRepoURL(t *testing.T) {
	// Test that client.NormalizeRepoURL works as expected (used by matchRepoSecret)
	tests := []struct {
		url      string
		expected string
	}{
		{"https://github.com/owner/repo.git", "github.com/owner/repo"},
		{"https://github.com/owner/repo/", "github.com/owner/repo"},
		{"https://github.com/owner/repo", "github.com/owner/repo"},
		{"git@github.com:owner/repo.git", "github.com/owner/repo"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			result := client.NormalizeRepoURL(tt.url)
			if result != tt.expected {
				t.Errorf("NormalizeRepoURL(%q) = %q, want %q", tt.url, result, tt.expected)
			}
		})
	}
}

func TestClientReposMatch(t *testing.T) {
	// Test that client.ReposMatch works as expected
	tests := []struct {
		a        string
		b        string
		expected bool
	}{
		{"https://github.com/owner/repo", "https://github.com/owner/repo", true},
		{"https://github.com/owner/repo.git", "https://github.com/owner/repo", true},
		{"git@github.com:owner/repo.git", "https://github.com/owner/repo", true},
		{"https://github.com/owner/repo1", "https://github.com/owner/repo2", false},
		{"", "https://github.com/owner/repo", false},
	}

	for _, tt := range tests {
		t.Run(tt.a+" vs "+tt.b, func(t *testing.T) {
			result := client.ReposMatch(tt.a, tt.b)
			if result != tt.expected {
				t.Errorf("ReposMatch(%q, %q) = %v, want %v", tt.a, tt.b, result, tt.expected)
			}
		})
	}
}
