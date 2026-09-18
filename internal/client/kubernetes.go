package client

import (
	"fmt"
	"strings"

	"github.com/akshatsinha007/kubuto/internal/config"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// NewKubeClient creates a new Kubernetes client using standard precedence rules.
func NewKubeClient(cfg *config.ClusterConfig) (kubernetes.Interface, error) {
	restConfig, err := buildRestConfig(cfg)
	if err != nil {
		return nil, err
	}
	return kubernetes.NewForConfig(restConfig)
}

// NewDynamicClient creates a new dynamic client for CRDs (like ArgoCD Applications).
func NewDynamicClient(cfg *config.ClusterConfig) (dynamic.Interface, error) {
	restConfig, err := buildRestConfig(cfg)
	if err != nil {
		return nil, err
	}
	return dynamic.NewForConfig(restConfig)
}

// buildLoadingRules constructs the kubeconfig loading rules and context
// overrides shared by every kubeconfig-derived resolution below (REST
// config, namespace) — one place for "how do we find the kubeconfig and
// context", so they can never drift apart from each other.
func buildLoadingRules(cfg *config.ClusterConfig) (*clientcmd.ClientConfigLoadingRules, *clientcmd.ConfigOverrides) {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	if cfg.Kubeconfig != "" {
		loadingRules.ExplicitPath = cfg.Kubeconfig
	}

	configOverrides := &clientcmd.ConfigOverrides{}
	if cfg.Context != "" {
		configOverrides.CurrentContext = cfg.Context
	}

	return loadingRules, configOverrides
}

// buildRestConfig builds a rest.Config from the cluster config.
func buildRestConfig(cfg *config.ClusterConfig) (*rest.Config, error) {
	loadingRules, configOverrides := buildLoadingRules(cfg)
	kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides)

	clientConfig, err := kubeConfig.ClientConfig()
	if err != nil {
		return nil, err
	}

	clientConfig.Timeout = cfg.Timeout

	return clientConfig, nil
}

// CurrentNamespace returns the effective namespace for the given cluster
// config — the same resolution kubectl itself uses: the current
// kubeconfig context's namespace, falling back to "default" only when the
// context genuinely has none set. This exists because hardcoding "default"
// (the previous behavior) is wrong for any user whose workloads live
// anywhere else, which is the common case.
func CurrentNamespace(cfg *config.ClusterConfig) (string, error) {
	loadingRules, configOverrides := buildLoadingRules(cfg)
	kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides)

	ns, _, err := kubeConfig.Namespace()
	if err != nil {
		return "", err
	}
	return ns, nil
}

// GetClusterK8sVersion auto-detects the Kubernetes version from the cluster API server.
func GetClusterK8sVersion(client kubernetes.Interface) (string, error) {
	if client == nil {
		return "", fmt.Errorf("kubernetes client is nil")
	}

	versionInfo, err := client.Discovery().ServerVersion()
	if err != nil {
		return "", fmt.Errorf("failed to get server version: %w", err)
	}

	// versionInfo.GitVersion looks like "v1.28.0-gke.1"
	// We want to extract "1.28" for compatibility checks
	fullVersion := versionInfo.GitVersion
	if len(fullVersion) > 0 && fullVersion[0] == 'v' {
		fullVersion = fullVersion[1:]
	}

	// Extract major.minor version (e.g., "1.28" from "1.28.0-gke.1")
	return extractMajorMinor(fullVersion), nil
}

// extractMajorMinor extracts the major.minor version from a semver string.
func extractMajorMinor(version string) string {
	// Split by dot and take first two components
	parts := strings.Split(version, ".")
	if len(parts) >= 2 {
		return parts[0] + "." + parts[1]
	}
	// Fallback: return as-is if parsing fails
	return version
}
