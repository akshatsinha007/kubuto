// Package scanner — version_helper.go
//
// `kubuto scan` is the "what's deployed and what's newer" command. It must
// answer that question for **every** scanner output (helm, flux, argocd)
// using only direct chart-repository fetches — never the Kubuto engine.
// The engine answers a *different* question ("is version X compatible
// with Kubernetes Y?"), which lives entirely in `kubuto compat ...`.
//
// This helper enforces that boundary: it ONLY does HTTP `index.yaml` reads
// against chart repositories the cluster already references, then applies
// the same `version.Resolve` + `version.Compare` math that `helm.go` has
// always used. Output columns end up identical across helm/flux/argocd.
package scanner

import (
	"context"
	"strings"

	"github.com/go-logr/logr"

	"github.com/akshatsinha007/kubuto/internal/client"
	"github.com/akshatsinha007/kubuto/internal/version"
	"github.com/akshatsinha007/kubuto/pkg/types"
)

// ResolveLatestFromIndex fetches `${repoURL}/index.yaml`, picks the
// highest semver matching `chartName`, and writes the resolved
// LatestVersion / LatestMinor / LatestPatch / UpdateStatus onto `res`.
//
// It is a no-op (returns early, leaves `res` untouched) when:
//
//   - `repoURL` is empty — no source to query.
//   - `repoURL` starts with `oci://` — OCI registries don't expose an
//     `index.yaml`; tag enumeration would need a different lookup entirely.
//   - `repoURL` looks like a git URL — Flux `GitRepository` sources, GitHub
//     SSH refs, etc. aren't Helm chart repos.
//   - `chartName` is empty — nothing to look up.
//   - `currentVersion` fails semver parsing — comparison would be meaningless.
//
// All failure modes are silent at info level and logged at V(1) — `scan`
// must keep running for every other row even when one repo is broken.
//
// This function never imports nor calls `internal/engine`. It does not
// authenticate, does not consume rate-limit quota, and does not need an
// API key.
func ResolveLatestFromIndex(
	ctx context.Context,
	hc *client.HelmClient,
	logger logr.Logger,
	res *types.Resource,
	repoURL, chartName, currentVersion string,
) {
	if hc == nil || res == nil {
		return
	}
	if !isHelmIndexableRepo(repoURL) || chartName == "" || currentVersion == "" {
		return
	}

	currentVer, err := version.Parse(currentVersion)
	if err != nil {
		logger.V(1).Info("scan: cannot parse current version, skipping latest resolution",
			"chart", chartName, "version", currentVersion, "error", err)
		return
	}

	versions, err := hc.GetChartVersions(ctx, repoURL, chartName)
	if err != nil {
		logger.V(1).Info("scan: failed to fetch index.yaml; LatestVersion left blank",
			"repo", repoURL, "chart", chartName, "error", err)
		return
	}

	parsed := version.StringsToVersions(versions)
	latest, latestMinor, latestPatch := version.Resolve(currentVer, parsed)

	if latest != nil {
		res.LatestVersion = latest.String()
		res.UpdateStatus = version.Compare(currentVer, latest)
	} else {
		res.UpdateStatus = types.UpdateStatusCurrent
	}
	if latestMinor != nil {
		res.LatestMinor = latestMinor.String()
	}
	if latestPatch != nil {
		res.LatestPatch = latestPatch.String()
	}
}

// isHelmIndexableRepo returns true only for repository URLs that we can
// expect to serve `/index.yaml`. We deliberately exclude OCI registries
// and git URLs — both are real source types that Flux and Argo can wire
// up, but neither responds to a Helm-style index fetch. Returning false
// here is what makes `ResolveLatestFromIndex` a graceful no-op for those
// scanner rows rather than producing spurious 404 log lines.
func isHelmIndexableRepo(u string) bool {
	if u == "" {
		return false
	}
	switch {
	case strings.HasPrefix(u, "oci://"):
		return false
	case strings.HasPrefix(u, "git@"):
		return false
	case strings.HasPrefix(u, "ssh://"):
		return false
	}
	// HelmRepository / Argo `repoURL` for charts is always http(s).
	return strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://")
}
