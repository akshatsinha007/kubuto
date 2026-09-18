package scanner

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"

	"github.com/akshatsinha007/kubuto/internal/client"
	"github.com/akshatsinha007/kubuto/pkg/types"
)

// fakeIndexYAML is a minimal index.yaml that exposes two charts so we can
// test both "chart found, newer version exists" and "chart absent" paths
// using the same fixture. Versions are intentionally not sorted in source
// order to confirm we depend on `version.Resolve`'s comparator rather
// than file order.
const fakeIndexYAML = `apiVersion: v1
entries:
  ingress-nginx:
    - name: ingress-nginx
      version: 4.7.1
    - name: ingress-nginx
      version: 4.10.2
    - name: ingress-nginx
      version: 4.9.0
  cert-manager:
    - name: cert-manager
      version: 1.13.2
    - name: cert-manager
      version: 1.14.0
`

// startFakeChartRepo serves /index.yaml for the version_helper tests.
// We track every request URL so individual cases can assert (or assert
// the absence of) outbound traffic.
func startFakeChartRepo(t *testing.T) (url string, hits *[]string) {
	t.Helper()
	requestURLs := make([]string, 0)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestURLs = append(requestURLs, r.URL.Path)
		if r.URL.Path == "/index.yaml" {
			w.Header().Set("Content-Type", "application/x-yaml")
			_, _ = w.Write([]byte(fakeIndexYAML))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, &requestURLs
}

func TestResolveLatestFromIndex_PopulatesLatestVersion(t *testing.T) {
	repoURL, hits := startFakeChartRepo(t)
	hc := client.NewHelmClient()

	res := &types.Resource{
		CurrentVersion: "4.7.1",
		UpdateStatus:   types.UpdateStatusUnknown,
	}

	ResolveLatestFromIndex(context.Background(), hc, logr.Discard(), res,
		repoURL, "ingress-nginx", "4.7.1")

	// 4.10.2 is the highest in fakeIndexYAML; 4.10 is a higher minor than
	// 4.7, so the diff classification must be "minor-available".
	assert.Equal(t, "4.10.2", res.LatestVersion,
		"expected highest semver in index.yaml to win")
	assert.Equal(t, types.UpdateStatusMinor, res.UpdateStatus,
		"expected minor-update classification for 4.7.1 → 4.10.2")
	assert.Len(t, *hits, 1, "exactly one index.yaml fetch expected")
	assert.Equal(t, "/index.yaml", (*hits)[0])
}

func TestResolveLatestFromIndex_CurrentIsLatest(t *testing.T) {
	repoURL, _ := startFakeChartRepo(t)
	hc := client.NewHelmClient()

	res := &types.Resource{
		CurrentVersion: "4.10.2",
		UpdateStatus:   types.UpdateStatusUnknown,
	}

	ResolveLatestFromIndex(context.Background(), hc, logr.Discard(), res,
		repoURL, "ingress-nginx", "4.10.2")

	// "Current" is the only signal here. We deliberately don't assert on
	// LatestVersion content because Resolve may return nil-latest when
	// the user is already at the top — UpdateStatus is the contract.
	assert.Equal(t, types.UpdateStatusCurrent, res.UpdateStatus)
}

func TestResolveLatestFromIndex_NoOpForOCI(t *testing.T) {
	repoURL, hits := startFakeChartRepo(t)
	_ = repoURL
	hc := client.NewHelmClient()

	res := &types.Resource{
		CurrentVersion: "4.7.1",
		UpdateStatus:   types.UpdateStatusUnknown,
	}

	// oci:// repos aren't index.yaml-shaped. The helper must short-circuit
	// without issuing any HTTP request.
	ResolveLatestFromIndex(context.Background(), hc, logr.Discard(), res,
		"oci://ghcr.io/example/charts", "ingress-nginx", "4.7.1")

	assert.Equal(t, "", res.LatestVersion)
	assert.Equal(t, types.UpdateStatusUnknown, res.UpdateStatus,
		"unknown must remain untouched when no upstream lookup happens")
	assert.Len(t, *hits, 0, "no HTTP traffic expected for oci:// sources")
}

func TestResolveLatestFromIndex_NoOpForGitURL(t *testing.T) {
	hc := client.NewHelmClient()
	res := &types.Resource{
		CurrentVersion: "1.0.0",
		UpdateStatus:   types.UpdateStatusUnknown,
	}

	for _, gitURL := range []string{
		"git@github.com:example/repo.git",
		"ssh://git@gitlab/example/repo.git",
		"",
	} {
		ResolveLatestFromIndex(context.Background(), hc, logr.Discard(), res,
			gitURL, "ingress-nginx", "1.0.0")
		assert.Equal(t, "", res.LatestVersion,
			"git/empty URL %q should leave LatestVersion blank", gitURL)
	}
}

func TestResolveLatestFromIndex_ChartMissingInIndex(t *testing.T) {
	repoURL, _ := startFakeChartRepo(t)
	hc := client.NewHelmClient()

	res := &types.Resource{
		CurrentVersion: "1.0.0",
		UpdateStatus:   types.UpdateStatusUnknown,
	}

	// Chart name not present in the fake index — helper must keep going
	// without panicking and leave Resource fields blank/Unknown.
	ResolveLatestFromIndex(context.Background(), hc, logr.Discard(), res,
		repoURL, "nonexistent-chart", "1.0.0")

	assert.Equal(t, "", res.LatestVersion)
	assert.Equal(t, types.UpdateStatusUnknown, res.UpdateStatus)
}

func TestResolveLatestFromIndex_BadCurrentVersion(t *testing.T) {
	repoURL, hits := startFakeChartRepo(t)
	hc := client.NewHelmClient()

	res := &types.Resource{
		CurrentVersion: "not-a-semver",
		UpdateStatus:   types.UpdateStatusUnknown,
	}

	ResolveLatestFromIndex(context.Background(), hc, logr.Discard(), res,
		repoURL, "ingress-nginx", "not-a-semver")

	// Helper must short-circuit BEFORE making the HTTP call: comparing a
	// non-semver "current" version against a list of semvers is
	// meaningless and would burn a network round-trip for nothing.
	assert.Equal(t, "", res.LatestVersion)
	assert.Equal(t, types.UpdateStatusUnknown, res.UpdateStatus)
	assert.Len(t, *hits, 0, "no HTTP traffic expected for unparseable currentVersion")
}

func TestResolveLatestFromIndex_NilGuards(t *testing.T) {
	// All-nil callers should be no-ops (defensive — flux.go and argocd.go
	// both rely on this). No panics, no test failures.
	ResolveLatestFromIndex(context.Background(), nil, logr.Discard(), nil,
		"https://charts.example.com", "ingress-nginx", "4.7.1")

	hc := client.NewHelmClient()
	ResolveLatestFromIndex(context.Background(), hc, logr.Discard(), nil,
		"https://charts.example.com", "ingress-nginx", "4.7.1")
}
