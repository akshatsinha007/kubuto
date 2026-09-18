# Contributing to kubuto

This file orients a contributor (human or AI) to how this repo is put
together and the rules that aren't obvious from reading one file in
isolation. For what the tool does and how to use it, see `README.md`.

## The one rule that matters most

**`scan` and `compat` are deliberately separate surfaces, and the
import graph enforces it, not just convention.** Nothing under
`internal/scanner/` imports `internal/engine`. `scan` must stay free
and engine-independent; if you're tempted to add an engine call inside
a scanner, that logic belongs in a `cmd/compat_*.go` command instead,
calling `internal/engine` directly after the scan has already run.

## Layout

```
cmd/                   Cobra commands. One file per command/subcommand.
internal/
  scanner/             Discovery: Helm releases, ArgoCD Applications,
                        Flux HelmReleases, container images. Never
                        imports internal/engine (see above).
  engine/              HTTP client for the compat engine. Only
                        cmd/compat_*.go calls into this package.
  client/              Thin wrappers around k8s/helm/git clients.
  output/              Table/JSON/YAML rendering, filtering, sorting.
  config/              Viper-backed config loading.
pkg/
  types/resource.go    The Resource struct every scanner emits — this
                        is the JSON/YAML output contract.
```

Each scanner (`argocd.go`, `flux.go`, `helm.go`, `images.go`) implements
the same `Scanner` interface (`internal/scanner/interface.go`) and
returns `[]types.Resource`. Read the doc comments in `argocd.go` and
`flux.go` directly for the GitOps-specific edge cases each one
handles (multi-source Applications, ApplicationSets, Flux's
`chartRef`/`targetNamespace`/`kubeConfig`) — that detail lives in the
code, intentionally not duplicated here where it could drift out of
sync.

## Conventions

- **New `types.Resource` fields must be `omitempty`.** The JSON/YAML
  shape is a documented contract (see `README.md`); additive fields are
  safe, anything else isn't.
- **Test files live next to the code they cover**, named for what
  they test (`argocd_destination_test.go`, `flux_chartref_test.go`),
  not for the PR or issue that introduced them.
- **Don't reintroduce duplicated logic across `argocd.go`/`flux.go`.**
  Shared behavior (namespace resolution, `index.yaml` latest-version
  lookup, `List()` pagination) already lives in
  `resolveTargetNamespaces`, `version_helper.go`, and `pagination.go`
  respectively — extend those instead of copying into a new scanner.

## Before sending a change

```bash
go build ./...
go vet ./...
gofmt -l .
go test ./...
```

CI runs the same four checks on every push and pull request
(`.github/workflows/ci.yml`). Releases are cut by pushing a `v*` tag —
see `.github/workflows/release.yml` and `.goreleaser.yml`.
