package helm

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"testing"

	helmchart "helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/release"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
)

// encodeReleaseSecretData reproduces exactly what Helm itself writes into a
// release Secret's "release" key (JSON -> gzip -> base64), so this test
// exercises the real wire format rather than a shortcut.
func encodeReleaseSecretData(t *testing.T, rel *release.Release) []byte {
	t.Helper()
	raw, err := json.Marshal(rel)
	if err != nil {
		t.Fatalf("marshal release: %v", err)
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(raw); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return []byte(base64.StdEncoding.EncodeToString(buf.Bytes()))
}

// releaseSecret builds a Secret shaped exactly like the ones Helm v3 writes
// for a given revision of a release.
func releaseSecret(t *testing.T, name, namespace string, revision int, status release.Status, chartVersion string) *corev1.Secret {
	t.Helper()
	rel := &release.Release{
		Name:      name,
		Namespace: namespace,
		Version:   revision,
		Info:      &release.Info{Status: status},
		Chart: &helmchart.Chart{
			Metadata: &helmchart.Metadata{Name: name, Version: chartVersion},
		},
	}
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("sh.helm.release.v1.%s.v%d", name, revision),
			Namespace: namespace,
			Labels: map[string]string{
				"owner":  "helm",
				"name":   name,
				"status": status.String(),
			},
		},
		Data: map[string][]byte{
			"release": encodeReleaseSecretData(t, rel),
		},
	}
}

func toRuntimeObjects(secrets []*corev1.Secret) []runtime.Object {
	objs := make([]runtime.Object, len(secrets))
	for i, s := range secrets {
		objs[i] = s
	}
	return objs
}

// TestClient_GetRelease_PicksDeployedRevision is the regression test for the
// exact risk this rewrite carries: a release that's been upgraded has one
// Secret per revision, with every earlier one marked "superseded" and
// exactly one "deployed". GetRelease must return the deployed revision's
// chart version, never a superseded one's — getting this wrong means a
// real user's `compat release` command confidently answers about a chart
// version they no longer have installed.
func TestClient_GetRelease_PicksDeployedRevision(t *testing.T) {
	const ns = "utils"
	const name = "kf-alb"

	// Mirrors the real kf-alb release found on the live cluster during
	// testing: multiple upgrades, only the latest is "deployed".
	secrets := []*corev1.Secret{
		releaseSecret(t, name, ns, 1, release.StatusSuperseded, "3.4.0"),
		releaseSecret(t, name, ns, 2, release.StatusDeployed, "3.5.0"),
	}

	client := fake.NewSimpleClientset(toRuntimeObjects(secrets)...)
	c := &Client{kube: client}

	rel, err := c.GetRelease(context.Background(), name, ns)
	if err != nil {
		t.Fatalf("GetRelease() error: %v", err)
	}
	if rel.Version != 2 {
		t.Errorf("expected revision 2 (the deployed one), got revision %d", rel.Version)
	}
	_, chartVersion := GetChartInfo(rel)
	if chartVersion != "3.5.0" {
		t.Errorf("expected chart version 3.5.0 (deployed), got %q (would be the superseded 3.4.0 if revision selection is wrong)", chartVersion)
	}
}

// TestClient_GetRelease_NoDeployedRevision covers a release that's been
// fully uninstalled (or every revision failed) — no secret has
// status=deployed. Must return a clear error, not silently pick a
// superseded/failed revision.
func TestClient_GetRelease_NoDeployedRevision(t *testing.T) {
	const ns = "default"
	const name = "gone"

	secrets := []*corev1.Secret{
		releaseSecret(t, name, ns, 1, release.StatusUninstalled, "1.0.0"),
	}
	client := fake.NewSimpleClientset(toRuntimeObjects(secrets)...)
	c := &Client{kube: client}

	_, err := c.GetRelease(context.Background(), name, ns)
	if err == nil {
		t.Fatal("expected an error when no revision is deployed, got nil")
	}
}

// TestClient_GetRelease_NotFound covers a release name that simply doesn't
// exist in the namespace.
func TestClient_GetRelease_NotFound(t *testing.T) {
	client := fake.NewSimpleClientset()
	c := &Client{kube: client}

	_, err := c.GetRelease(context.Background(), "nope", "default")
	if err == nil {
		t.Fatal("expected an error for a nonexistent release, got nil")
	}
}
