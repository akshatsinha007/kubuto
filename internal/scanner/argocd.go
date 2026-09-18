package scanner

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/akshatsinha007/kubuto/internal/client"
	"github.com/akshatsinha007/kubuto/pkg/types"
)

var (
	applicationGVR = schema.GroupVersionResource{
		Group:    "argoproj.io",
		Version:  "v1alpha1",
		Resource: "applications",
	}
)

// ArgoCDScanner scans ArgoCD Application CRDs for Helm charts.
//
// Boundary contract: this scanner is part of the `kubuto scan` path. It
// MUST NOT call the Kubuto engine — that's the job of `kubuto compat`.
// We deliberately do not hold an `*engine.Client` here so the import
// graph itself prevents reintroducing engine calls. K8s-version
// compatibility checks live in `cmd/compat_cluster.go`.
type ArgoCDScanner struct {
	kubeClient    kubernetes.Interface
	dynamicClient dynamic.Interface
	helmClient    *client.HelmClient // for index.yaml fetches; latest-version resolution
	gitClient     *client.GitClient
	fetchMethod   string // "api" or "clone"
	logger        logr.Logger
}

// NewArgoCDScanner creates a new ArgoCD scanner.
//
// `helmClient` is required for the LatestVersion column to be populated;
// callers may pass nil only in tests asserting the no-upstream-lookup
// path. Production wiring is in `cluster_scan.go`.
func NewArgoCDScanner(kc kubernetes.Interface, dc dynamic.Interface, hc *client.HelmClient, fetchMethod string, logger logr.Logger) *ArgoCDScanner {
	return &ArgoCDScanner{
		kubeClient:    kc,
		dynamicClient: dc,
		helmClient:    hc,
		fetchMethod:   fetchMethod,
		logger:        logger,
	}
}

// Name returns the scanner name.
func (s *ArgoCDScanner) Name() string {
	return "ArgoCD"
}

// Validate checks if the scanner can run.
func (s *ArgoCDScanner) Validate(ctx context.Context) error {
	if s.kubeClient == nil {
		return fmt.Errorf("kubernetes client is not initialized")
	}
	if s.dynamicClient == nil {
		return fmt.Errorf("dynamic client is not initialized")
	}
	return nil
}

// Cleanup performs any cleanup actions.
func (s *ArgoCDScanner) Cleanup() error {
	return nil
}

// Scan discovers ArgoCD applications and checks for updates.
//
// Namespace selection follows a strict precedence so users get exactly
// the surface they asked for, and never an empty result by accident:
//
//  1. `--gitops-namespace foo` (single) wins and constrains the List to
//     that one namespace.
//  2. Otherwise, `--namespace ns1,ns2` is honoured by listing each
//     namespace in turn and concatenating results.
//  3. Otherwise we list cluster-wide, since ArgoCD Applications commonly
//     live outside a fixed `argocd` namespace.
func (s *ArgoCDScanner) Scan(ctx context.Context, opts ScanOptions) ([]types.Resource, error) {
	var resources []types.Resource

	// Always provision a GitClient. The git fetch path now picks per-
	// repo credentials via `resolveGitAuth(repoURL, repoSecrets)` —
	// inheriting them from ArgoCD's own `secret-type=repository` /
	// `secret-type=repo-creds` secrets — so users do not need to
	// duplicate credentials in `scanning.gitops` to get Current/Latest
	// for git-source ArgoCD apps.
	//
	// `opts.GitOpsConfigs` is still honoured as a manual override / for
	// repos ArgoCD itself doesn't track (e.g. an unrelated public mirror
	// the user wants kubuto to follow). The first such entry's Token
	// becomes the default fallback when no per-repo secret matches.
	var defaultToken string
	if len(opts.GitOpsConfigs) > 0 {
		defaultToken = opts.GitOpsConfigs[0].Token
	}
	if s.gitClient == nil {
		s.gitClient = client.NewGitClient(defaultToken)
	}

	namespaces := resolveTargetNamespaces(opts)

	// Pre-fetch repo secrets across the same namespace set. Repo
	// secrets are typically colocated with the ArgoCD installation
	// (one namespace), but we keep the list consistent with the apps
	// we scan so a `--namespace` filter narrows both equally.
	var repoSecrets []repoSecret
	for _, ns := range namespaces {
		secs, err := s.listRepoSecrets(ctx, ns)
		if err != nil {
			s.logger.V(1).Info("Failed to list repo secrets, git sources will need --gitops",
				"namespace", ns, "error", err)
			continue
		}
		repoSecrets = append(repoSecrets, secs...)
	}

	for _, ns := range namespaces {
		apps, err := paginatedUnstructuredList(ctx, s.dynamicClient.Resource(applicationGVR).Namespace(ns).List, metav1.ListOptions{})
		if err != nil {
			// Per-namespace error: log and continue rather than abort
			// the whole scan. A user with namespace-scoped RBAC may
			// legitimately have read access to some namespaces and
			// not others; we'd rather surface partial results than
			// nothing.
			scope := ns
			if scope == "" {
				scope = "cluster-wide"
			}
			s.logger.V(1).Info("Failed to list ArgoCD applications", "scope", scope, "error", err)
			continue
		}
		for _, app := range apps {
			// processApplication may return multiple resources when
			// the app is a git-source umbrella chart with several
			// dependencies; each dependency surfaces as its own scan
			// row so the user can see per-chart compat verdicts
			// instead of a single aggregate.
			appRes, err := s.processApplication(ctx, app, opts, repoSecrets)
			if err != nil {
				s.logger.V(1).Info("Error processing application", "app", app.GetName(), "error", err)
				continue
			}
			resources = append(resources, appRes...)
		}
	}

	return resources, nil
}

// repoSecret holds parsed ArgoCD repository / repo-creds secret data.
//
// We carry the credential fields through so the scanner can mint a
// per-call `client.GitAuth` for git-source ArgoCD apps without ever
// asking the user to duplicate creds in their kubuto config — ArgoCD
// already has them, kubuto simply reads them.
//
// `IsCreds` distinguishes the two ArgoCD secret flavours:
//   - secret-type=repository → URL is a literal repo URL (exact match)
//   - secret-type=repo-creds → URL is a *prefix template* (longest-prefix
//     match across many repos under the same provider account)
//
// We deliberately do NOT store SSH private keys or GitHub App private
// keys. Carrying them invites an exfiltration pathway through scan
// output / logs and the kubuto-scan code path can't use them anyway
// (Chart.yaml fetch is HTTPS-only). A repo whose ArgoCD secret only
// has `sshPrivateKey` falls back to the `git@<shortSHA>` row.
type repoSecret struct {
	Name     string
	URL      string
	Type     string // "git" | "helm" | "" (older clusters)
	IsCreds  bool   // true when sourced from secret-type=repo-creds
	Username string
	Password string
}

// auth converts a repoSecret into the per-call credential bundle
// expected by `client.GitClient.FetchChartYamlWithAuth`. Only HTTPS-
// usable fields (Username, Password) are forwarded. Returns the
// zero-value when no usable credentials are present, which the fetcher
// treats as "send no Authorization header".
func (rs repoSecret) auth() client.GitAuth {
	return client.GitAuth{Username: rs.Username, Password: rs.Password}
}

// listRepoSecrets fetches ArgoCD repository AND repo-creds secrets from
// the cluster, keeping both flavours with their credential fields so
// `resolveGitAuth` can mint per-call `client.GitAuth` values for git
// sources, not just `type=helm` ones.
//
// Two label selectors are queried in turn: clusters that ship pre-2.5
// ArgoCD only have `secret-type=repository`; modern installations also
// expose `secret-type=repo-creds` for URL-prefix templates. Errors on
// the repo-creds list are non-fatal (the scan still works on `repository`
// secrets alone).
func (s *ArgoCDScanner) listRepoSecrets(ctx context.Context, namespace string) ([]repoSecret, error) {
	repoSecrets, err := paginatedSecretList(ctx, s.kubeClient.CoreV1().Secrets(namespace).List, metav1.ListOptions{
		LabelSelector: "argocd.argoproj.io/secret-type=repository",
	})
	if err != nil {
		return nil, err
	}

	var result []repoSecret
	for _, secret := range repoSecrets {
		result = append(result, parseRepoSecret(secret.Name, secret.Data, false))
	}

	credsSecrets, err := paginatedSecretList(ctx, s.kubeClient.CoreV1().Secrets(namespace).List, metav1.ListOptions{
		LabelSelector: "argocd.argoproj.io/secret-type=repo-creds",
	})
	if err == nil {
		for _, secret := range credsSecrets {
			result = append(result, parseRepoSecret(secret.Name, secret.Data, true))
		}
	} else {
		s.logger.V(1).Info("Failed to list repo-creds secrets (non-fatal)",
			"namespace", namespace, "error", err)
	}

	return result, nil
}

// parseRepoSecret turns a Kubernetes secret payload into a repoSecret.
// Extracted so the same parsing applies to both repository and
// repo-creds secrets (their schemas overlap in the fields kubuto cares
// about).
func parseRepoSecret(name string, data map[string][]byte, isCreds bool) repoSecret {
	return repoSecret{
		Name:     name,
		URL:      string(data["url"]),
		Type:     string(data["type"]),
		IsCreds:  isCreds,
		Username: string(data["username"]),
		Password: string(data["password"]),
	}
}

// appMeta bundles the per-Application metadata that every source shape
// (direct Helm, multi-source, git-source) attaches to its resulting
// Resource(s) unchanged — destination cluster info and the owning
// ApplicationSet, if any.
type appMeta struct {
	destServer, destNamespace, generatedBy string
}

// processApplication processes a single ArgoCD Application CRD. It
// returns a slice because git-source umbrella charts can pull in many
// dependencies, and each one is reported as its own scan row so the user
// can see per-chart compat verdicts. Direct Helm sources still return a
// single-element slice.
func (s *ArgoCDScanner) processApplication(ctx context.Context, app unstructured.Unstructured, opts ScanOptions, repoSecrets []repoSecret) ([]types.Resource, error) {
	destServer, destNamespace := destinationFields(app)
	meta := appMeta{destServer, destNamespace, applicationSetOwner(app)}

	source, found, err := unstructured.NestedMap(app.Object, "spec", "source")
	if err != nil || !found {
		// ArgoCD 2.6+ Applications may use spec.sources (plural) instead
		// of the singular spec.source — and when sources is set, ArgoCD
		// itself ignores the singular field entirely. Without this check,
		// a multi-source Application would vanish from scan output
		// entirely instead of being reported.
		sources, sFound, sErr := unstructured.NestedSlice(app.Object, "spec", "sources")
		if sErr == nil && sFound && len(sources) > 0 {
			return s.processMultiSource(ctx, app.GetName(), app.GetNamespace(), sources, meta)
		}
		return nil, fmt.Errorf("no source found in application %s", app.GetName())
	}

	chart, _ := source["chart"].(string)
	targetRevision, _ := source["targetRevision"].(string)
	repoURL, _ := source["repoURL"].(string)

	if chart == "" {
		// Type 2: Git source — no direct chart reference. Walk Chart.yaml
		// dependencies if a matching --gitops config is present.
		return s.processGitSource(ctx, app, source, opts, repoSecrets, meta)
	}

	// Type 1: Direct Helm source. Always exactly one row.
	res, err := s.processDirectHelmSource(ctx, app.GetName(), app.GetNamespace(), chart, targetRevision, repoURL, meta)
	if err != nil {
		return nil, err
	}
	if res == nil {
		return nil, nil
	}
	return []types.Resource{*res}, nil
}

// destinationFields reads spec.destination.server/.name/.namespace off an
// ArgoCD Application. Applications set either .server (a raw API server
// URL) or .name (a friendly alias registered via an ArgoCD `cluster`
// secret) — rarely both — so .name is used as a fallback when .server is
// blank rather than being a separate field, keeping exactly one
// "destination cluster identity" string per resource.
func destinationFields(app unstructured.Unstructured) (server, namespace string) {
	dest, found, err := unstructured.NestedMap(app.Object, "spec", "destination")
	if err != nil || !found {
		return "", ""
	}
	server, _ = dest["server"].(string)
	if server == "" {
		server, _ = dest["name"].(string)
	}
	namespace, _ = dest["namespace"].(string)
	return server, namespace
}

// applicationSetOwner returns the name of the ArgoCD ApplicationSet that
// generated this Application, if any. ApplicationSet-generated
// Applications carry a genuine `ownerReferences` entry with
// `kind: ApplicationSet` (set by the applicationset-controller itself) —
// unlike a label, this can't be spoofed by hand-editing an Application and
// is the reliable signal to key off. Returns "" for a standalone
// Application (the common case) or one owned by anything else.
func applicationSetOwner(app unstructured.Unstructured) string {
	for _, ref := range app.GetOwnerReferences() {
		if ref.Kind == "ApplicationSet" {
			return ref.Name
		}
	}
	return ""
}

// processMultiSource handles ArgoCD 2.6+ Applications that use
// spec.sources (plural). Each entry gets fanned out as its own row (one
// per source), named `<app>/<chart>` so multiple rows sharing a parent
// Application stay visibly linked — same convention as processGitSource's
// dependency fan-out (`<app>/<dep>`). A source entry with no `chart` set
// (e.g. a values-only companion source combined with a chart source, or a
// second git-path source) has no version of its own to report and is
// skipped, rather than emitting a meaningless blank row — mirroring how
// the dependency fan-out already skips deps with no name/repo.
func (s *ArgoCDScanner) processMultiSource(ctx context.Context, name, ns string, sources []interface{}, meta appMeta) ([]types.Resource, error) {
	out := make([]types.Resource, 0, len(sources))
	for _, raw := range sources {
		src, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}

		chart, _ := src["chart"].(string)
		if chart == "" {
			s.logger.V(1).Info("Skipping chartless source entry in multi-source application",
				"app", name, "repoURL", src["repoURL"])
			continue
		}

		targetRevision, _ := src["targetRevision"].(string)
		repoURL, _ := src["repoURL"].(string)

		res := types.Resource{
			Name:                 fmt.Sprintf("%s/%s", name, chart),
			Namespace:            ns,
			Type:                 types.ResourceTypeArgoApp,
			CurrentVersion:       targetRevision,
			Repository:           repoURL,
			UpdateStatus:         types.UpdateStatusUnknown,
			DestinationServer:    meta.destServer,
			DestinationNamespace: meta.destNamespace,
			GeneratedBy:          meta.generatedBy,
		}

		// Latest-version lookup against this source's own repo — same
		// path as a single-source Direct Helm application.
		ResolveLatestFromIndex(ctx, s.helmClient, s.logger, &res, repoURL, chart, targetRevision)

		out = append(out, res)
	}

	if len(out) == 0 {
		s.logger.V(1).Info("Multi-source application has no chart-bearing sources", "app", name)
	}

	return out, nil
}

// processDirectHelmSource handles Type 1: direct Helm chart references.
//
// Argo `spec.source.repoURL` for direct Helm sources is always an
// http(s) chart repo (or oci://); both cases are handled correctly by
// `ResolveLatestFromIndex` (the latter as a no-op).
func (s *ArgoCDScanner) processDirectHelmSource(ctx context.Context, name, ns, chart, version, repoURL string, meta appMeta) (*types.Resource, error) {
	res := types.Resource{
		Name:                 name,
		Namespace:            ns,
		Type:                 types.ResourceTypeArgoApp,
		CurrentVersion:       version,
		Repository:           repoURL,
		UpdateStatus:         types.UpdateStatusUnknown,
		DestinationServer:    meta.destServer,
		DestinationNamespace: meta.destNamespace,
		GeneratedBy:          meta.generatedBy,
	}

	// Resolve LatestVersion via direct index.yaml fetch — same path
	// `helm.go` uses. Engine is intentionally NOT consulted here.
	ResolveLatestFromIndex(ctx, s.helmClient, s.logger, &res, repoURL, chart, version)

	return &res, nil
}

// processGitSource handles Type 2: git-based sources whose chart definition
// lives in a Chart.yaml in the GitOps repo. We fan out one Resource per
// dependency listed in the umbrella Chart.yaml so users can see each
// underlying chart's compat verdict separately. Previous behaviour was to
// process only the first dependency and silently ignore the rest, hiding
// real incompatibility for n-1 charts.
func (s *ArgoCDScanner) processGitSource(ctx context.Context, app unstructured.Unstructured, source map[string]interface{}, opts ScanOptions, repoSecrets []repoSecret, meta appMeta) ([]types.Resource, error) {
	name := app.GetName()
	ns := app.GetNamespace()
	path, _ := source["path"].(string)
	repoURL, _ := source["repoURL"].(string)
	targetRevision, _ := source["targetRevision"].(string)

	// "umbrella" is the fallback row emitted whenever we can't (or
	// shouldn't) fan out into per-dependency rows. Its CurrentVersion
	// tells the user which case they're in: the real chart.yaml version
	// (fetch succeeded, no fanout needed), a `git@<shortSHA>` pin (fetch
	// failed — private/SSH-only repo, network error, 404), or blank with
	// CompatError set (root cause, credentials scrubbed by
	// sanitiseFetchError before reaching the table).
	umbrella := types.Resource{
		Name:                 name,
		Namespace:            ns,
		Type:                 types.ResourceTypeArgoApp,
		Repository:           repoURL,
		CompatStatus:         types.CompatStatusNeedsGitOps,
		UpdateStatus:         types.UpdateStatusUnknown,
		DestinationServer:    meta.destServer,
		DestinationNamespace: meta.destNamespace,
		GeneratedBy:          meta.generatedBy,
	}

	if matched := matchRepoSecret(repoURL, repoSecrets); matched != "" {
		umbrella.Repository = matched
	}

	// Auth is sourced per-repo from ArgoCD's own secrets first, the
	// manual `scanning.gitops` overrides second, falling back to
	// anonymous HTTPS for public repos — so a fetch is always attempted
	// rather than only when a `--gitops` config happens to be present.
	if s.gitClient == nil {
		// Defensive: Scan() always provisions one. Keeping the guard so
		// unit tests that bypass Scan() (or future callers) still get a
		// safe degraded result instead of a nil deref.
		return []types.Resource{umbrella}, nil
	}

	auth := s.resolveGitAuth(repoURL, repoSecrets, opts)
	branch := s.resolveGitBranch(repoURL, opts, targetRevision)

	chartYaml, fetchErr := s.gitClient.FetchChartYamlWithAuth(ctx, repoURL, path, branch, auth)
	if fetchErr != nil {
		s.logger.V(1).Info("Failed to fetch Chart.yaml",
			"app", name, "repo", repoURL, "branch", branch, "error", fetchErr)
		umbrella.CurrentVersion = gitRevisionLabel(targetRevision)
		umbrella.CompatError = sanitiseFetchError(fetchErr, auth)
		return []types.Resource{umbrella}, nil
	}

	// Successful fetch: surface the umbrella's own version even when
	// there are no dependencies, so the table cell is no longer blank.
	umbrella.CurrentVersion = chartYaml.Version

	if len(chartYaml.Dependencies) == 0 {
		s.logger.V(1).Info("No dependencies in Chart.yaml", "app", name)
		return []types.Resource{umbrella}, nil
	}

	// Resolve the merged Helm values the user actually deployed against,
	// so we can honour each dep's `condition` / `tags` and skip subcharts
	// that aren't enabled (e.g. an optional `minio` left at default
	// `minio.enabled: false`). Without this filter the scan output would
	// flag charts the user never installed — pure noise.
	values := s.extractHelmValues(source)

	// Fan out: emit one row per *enabled* dependency. Names are
	// synthesized as `<app>/<dep>` so reports stay grouped by parent.
	out := make([]types.Resource, 0, len(chartYaml.Dependencies))
	for _, dep := range chartYaml.Dependencies {
		if dep.Repository == "" || dep.Name == "" {
			continue
		}
		if !isDepEnabled(dep.Condition, dep.Tags, values) {
			s.logger.V(1).Info("Skipping disabled subchart",
				"app", name, "dep", dep.Name, "condition", dep.Condition, "tags", dep.Tags)
			continue
		}

		upstreamRepo := matchRepoSecret(dep.Repository, repoSecrets)
		if upstreamRepo == "" {
			upstreamRepo = dep.Repository
		}

		depRes := types.Resource{
			Name:                 fmt.Sprintf("%s/%s", name, dep.Name),
			Namespace:            ns,
			Type:                 types.ResourceTypeArgoApp,
			CurrentVersion:       dep.Version,
			Repository:           upstreamRepo,
			UpdateStatus:         types.UpdateStatusUnknown,
			DestinationServer:    meta.destServer,
			DestinationNamespace: meta.destNamespace,
			GeneratedBy:          meta.generatedBy,
		}

		// Latest-version lookup against the dependency's own repo
		// (not the umbrella git source). No engine call.
		ResolveLatestFromIndex(ctx, s.helmClient, s.logger, &depRes, upstreamRepo, dep.Name, dep.Version)

		out = append(out, depRes)
	}

	if len(out) == 0 {
		// Either every dep had blank repo/name OR every dep was
		// condition-disabled. Either way the umbrella row is the most
		// honest thing we can return — the user sees "we found the app,
		// but nothing inside is currently active".
		return []types.Resource{umbrella}, nil
	}
	return out, nil
}

// extractHelmValues pulls together the user-supplied Helm values from
// an ArgoCD `spec.source.helm` block. Argo lets users specify values in
// three overlapping ways and we honour Helm's own precedence (later
// wins): `values` (string YAML), `valuesObject` (already-structured map),
// and `parameters` (CLI-style `--set foo=bar` overrides).
//
// We deliberately do NOT chase `valueFiles` — those reference files
// inside the gitops repo which would mean another network round-trip per
// file, plus path-resolution headaches. If a user puts their
// `condition`-driving toggles in a `valueFiles` entry we'll
// conservatively assume the dep is enabled, matching pre-existing
// behaviour. Documented so future-us can revisit if needed.
func (s *ArgoCDScanner) extractHelmValues(source map[string]interface{}) map[string]any {
	helmBlock, _ := source["helm"].(map[string]interface{})
	if helmBlock == nil {
		return map[string]any{}
	}

	// 1. Inline YAML string.
	rawValues, _ := helmBlock["values"].(string)

	// 2. Structured object (already a map, no parse needed).
	valuesObject, _ := helmBlock["valuesObject"].(map[string]interface{})

	merged, err := mergeValuesYAML(rawValues)
	if err != nil {
		s.logger.V(1).Info("Failed to parse spec.source.helm.values, treating as empty",
			"error", err)
		merged = map[string]any{}
	}
	if valuesObject != nil {
		merged = mergeMaps(merged, normaliseAnyMap(valuesObject))
	}

	// 3. CLI-style parameter overrides; Helm processes these last.
	if rawParams, ok := helmBlock["parameters"].([]interface{}); ok {
		for _, p := range rawParams {
			pm, ok := p.(map[string]interface{})
			if !ok {
				continue
			}
			n, _ := pm["name"].(string)
			v, _ := pm["value"].(string)
			if n == "" {
				continue
			}
			setDottedPath(merged, n, v)
		}
	}

	return merged
}

// normaliseAnyMap converts the unstructured dynamic-client representation
// (`map[string]interface{}`) into the `map[string]any` shape our value
// helpers expect. Same underlying type, but separating the two
// declarations keeps the public helper API compatible with callers that
// hand-build values maps in tests.
func normaliseAnyMap(in map[string]interface{}) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		switch nested := v.(type) {
		case map[string]interface{}:
			out[k] = normaliseAnyMap(nested)
		default:
			out[k] = v
		}
	}
	return out
}

// setDottedPath plants `value` at the dotted location `key` inside
// `into`, creating intermediate maps as needed. Mirrors what `helm
// install --set foo.bar=baz` does. We store the value as a string
// because Argo's parameter type is always `string`; downstream
// `truthy()` already handles "true"/"false" coercion.
func setDottedPath(into map[string]any, key, value string) {
	parts := strings.Split(key, ".")
	cur := into
	for i, p := range parts {
		if i == len(parts)-1 {
			cur[p] = value
			return
		}
		next, ok := cur[p].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[p] = next
		}
		cur = next
	}
}

// matchRepoSecret tries to match a gitops repo URL against ArgoCD repo
// secrets. We use prefix matching with a path-boundary check rather than
// raw substring containment because:
//
//  1. `https://charts.example.com/foo` should match `https://charts.example.com/`
//     (a path within a tracked repo).
//  2. `https://charts.example.com/foo` must NOT match `https://charts.example.com/foobar`
//     (different repos that happen to share a prefix). Plain
//     strings.Contains historically caused false positives there, e.g.
//     short URLs like `https://x` accidentally matching every secret.
//
// Both URLs are first NormalizeRepoURL'd to strip schemes, trailing
// slashes, and `.git` suffixes so that the comparison happens on canonical
// path-form text.
func matchRepoSecret(gitopsURL string, secrets []repoSecret) string {
	if gitopsURL == "" {
		return ""
	}

	normalizedGitops := client.NormalizeRepoURL(gitopsURL)

	for _, sec := range secrets {
		if sec.URL == "" || sec.Type != "helm" {
			continue
		}

		normalizedSecret := client.NormalizeRepoURL(sec.URL)

		if normalizedGitops == normalizedSecret {
			return sec.URL
		}

		// Path-boundary prefix match in either direction. The boundary
		// check (`==` end-of-string OR next char is '/') prevents the
		// `foo` → `foobar` false-positive.
		if hasURLPrefix(normalizedGitops, normalizedSecret) ||
			hasURLPrefix(normalizedSecret, normalizedGitops) {
			return sec.URL
		}
	}
	return ""
}

// hasURLPrefix reports whether `longer` begins with `shorter` such that
// the next character (if any) is a '/' — i.e. `shorter` is a strict
// path-ancestor of `longer`. Empty inputs always return false.
func hasURLPrefix(longer, shorter string) bool {
	if shorter == "" || longer == "" || len(shorter) > len(longer) {
		return false
	}
	if !strings.HasPrefix(longer, shorter) {
		return false
	}
	if len(longer) == len(shorter) {
		return true
	}
	return longer[len(shorter)] == '/'
}
