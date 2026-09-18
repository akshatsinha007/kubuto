package helm

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"

	"helm.sh/helm/v3/pkg/release"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/akshatsinha007/kubuto/internal/client"
	"github.com/akshatsinha007/kubuto/internal/config"
)

// Client reads Helm release metadata directly from the release-storage
// Secrets Helm itself writes, using a plain Kubernetes clientset — the same
// approach internal/scanner/helm.go already uses and this project verified
// correct against a real cluster.
//
// This deliberately avoids the Helm SDK's action/kube packages (previously
// used here via action.Configuration/action.Get). Those pull in
// k8s.io/cli-runtime, k8s.io/kubectl, and — transitively — docker/containerd,
// which accounted for most of this binary's size for a feature that only
// ever needs to read one already-deployed release; it never installs,
// upgrades, or rolls one back.
type Client struct {
	kube kubernetes.Interface
}

// NewClient creates a new Helm client using standard kubeconfig discovery.
// kubeconfigPath and kubeContext may be empty, in which case standard
// discovery rules apply (current context, default kubeconfig path) — same
// precedence as internal/client.NewKubeClient and kubectl itself.
func NewClient(kubeconfigPath, kubeContext string) (*Client, error) {
	kc, err := client.NewKubeClient(&config.ClusterConfig{
		Kubeconfig: kubeconfigPath,
		Context:    kubeContext,
	})
	if err != nil {
		return nil, fmt.Errorf("build kubernetes client: %w", err)
	}
	return &Client{kube: kc}, nil
}

// GetRelease finds the currently-deployed revision of a named Helm release
// in a namespace. Mirrors internal/scanner/helm.go's proven approach: list
// the release-storage Secrets Helm writes (label `owner=helm`, and `name=`
// the release — both are part of Helm v3's stable, documented secrets
// storage driver format), decode each, and return the one whose status is
// "deployed". Helm's own invariant is that exactly one revision holds that
// status at a time, so this never needs to compare revision numbers
// itself — it trusts the same status field Helm maintains.
func (c *Client) GetRelease(ctx context.Context, name, namespace string) (*release.Release, error) {
	secrets, err := c.kube.CoreV1().Secrets(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "owner=helm,name=" + name,
	})
	if err != nil {
		return nil, fmt.Errorf("list helm release secrets: %w", err)
	}

	for _, secret := range secrets.Items {
		rel, err := decodeReleaseSecret(secret.Data["release"])
		if err != nil {
			continue
		}
		// Defensive re-check even though the label selector already
		// filtered by name — cheap, and avoids trusting label matching
		// alone for a security/correctness-relevant lookup.
		if rel.Name != name {
			continue
		}
		if rel.Info != nil && rel.Info.Status == release.StatusDeployed {
			return rel, nil
		}
	}
	return nil, fmt.Errorf("no deployed release named %q found in namespace %q", name, namespace)
}

// decodeReleaseSecret decodes the gzipped, base64-encoded release data Helm
// stores in a release Secret's "release" key. Intentionally mirrors
// internal/scanner/helm.go's decodeRelease — same format, same proven
// logic — rather than sharing a helper across the internal/pkg boundary
// for one small function.
func decodeReleaseSecret(data []byte) (*release.Release, error) {
	b, err := base64.StdEncoding.DecodeString(string(data))
	if err != nil {
		return nil, fmt.Errorf("base64 decode release data: %w", err)
	}

	r, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("create gzip reader: %w", err)
	}
	defer r.Close()

	d, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("decompress release data: %w", err)
	}

	var rel release.Release
	if err := json.Unmarshal(d, &rel); err != nil {
		return nil, fmt.Errorf("unmarshal release JSON: %w", err)
	}
	return &rel, nil
}

// GetChartInfo extracts chart name and version from a release
func GetChartInfo(rel *release.Release) (chartName, chartVersion string) {
	if rel.Chart != nil {
		chartName = rel.Chart.Name()
		chartVersion = rel.Chart.Metadata.Version
	}
	return chartName, chartVersion
}
