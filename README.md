# kubuto

[![CI](https://github.com/akshatsinha007/kubuto/actions/workflows/ci.yml/badge.svg)](https://github.com/akshatsinha007/kubuto/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/akshatsinha007/kubuto)](https://github.com/akshatsinha007/kubuto/releases)
[![License](https://img.shields.io/github/license/akshatsinha007/kubuto)](LICENSE)

**A CLI that tells you what's actually deployed in your cluster, and how far behind it is.**

`kubuto scan` walks your Helm releases, ArgoCD `Application`s, and Flux `HelmRelease`s in one pass and reports each chart's current vs. latest upstream version — no separate `argocd`/`flux`/`helm` scripts glued together by hand.

```
$ kubuto scan --all
+----------------+-----------+--------------------+-----------------+--------+------------------+
| NAME           | NAMESPACE | TYPE               | CURRENT VERSION | LATEST | STATUS           |
+----------------+-----------+--------------------+-----------------+--------+------------------+
| ingress-nginx  | default   | helm-chart         | 4.10.0          | 4.15.1 | minor-available  |
| cert-manager   | argocd    | argocd-application | v1.15.3         | 1.21.2 | minor-available  |
| kf-alb         | utils     | helm-chart         | 3.5.0           |        | current          |
+----------------+-----------+--------------------+-----------------+--------+------------------+
```

## Install

**Homebrew:**
```bash
brew install akshatsinha007/kubuto/kubuto
```

**Install script** (detects your OS/arch, installs the matching release binary):
```bash
curl -sSL https://raw.githubusercontent.com/akshatsinha007/kubuto/main/install.sh | sh
```
Covers `darwin`/`linux` on `amd64`/`arm64`. Installs to `/usr/local/bin` by default — override with `KUBUTO_INSTALL_DIR`, or pin a version with `KUBUTO_VERSION=v0.1.0`. Or just grab the archive for your platform directly from the [releases page](https://github.com/akshatsinha007/kubuto/releases/latest).

**From source** (Go 1.24+):
```bash
git clone https://github.com/akshatsinha007/kubuto.git
cd kubuto && go build -o /usr/local/bin/kubuto ./cmd/kubuto
```

## Quick start

```bash
export KUBECONFIG=~/.kube/config     # kubuto uses whatever cluster kubectl would talk to

kubuto scan                          # every Helm release + its latest upstream version
kubuto scan --all                    # also include ArgoCD- and Flux-deployed charts
kubuto scan --filter major-available # only what has a major version bump waiting
kubuto scan -o json | jq '.resources[].name'
```

## Commands

| Command | What it does |
|---|---|
| `kubuto scan` | Discovers deployed Helm/ArgoCD/Flux resources and reports each one's current vs. latest upstream version. |
| `kubuto config init` / `show` | Write or inspect the effective config (`~/.kubuto/config.yaml`). |
| `kubuto compat ...` | Checks whether a chart version is compatible with a target Kubernetes version. **Coming in v2** — not yet usable out of the box. |

Run `kubuto <command> --help` for the full flag reference on any of these — `scan` in particular has quite a few (namespace/GitOps scoping, output format, filtering, sorting).

## Configuration

Resolved in this order (later wins): built-in defaults → `~/.kubuto/config.yaml` (or `--config <path>`) → `KUBUTO_*` environment variables → CLI flags.

```yaml
scanning:
  argocd:
    enabled: true
  flux:
    enabled: true
```

See `example-config.yaml` for a complete example.

## How version resolution works

For each discovered resource, `kubuto` fetches its chart repository's `index.yaml` once and picks the highest non-prerelease version. This is deliberately conservative: an OCI-based chart, a raw git source, or a private repo it can't reach anonymously all produce an honest blank/`unknown` rather than a guess — `kubuto` will tell you explicitly when this happens rather than leaving a cell mysteriously empty.

## Troubleshooting

**Most rows show `status=unknown`.** The chart was likely installed from a private repository `kubuto` can't reach. Configure a mirror, or accept the gap.

**Flux or ArgoCD scan finds nothing.** Pass `--all` or `--gitops <provider>` explicitly — GitOps scanning isn't on by default with a plain `kubuto scan`.

## Contributing

See [`AGENTS.md`](AGENTS.md) for repo layout and conventions.

## License

[Apache License 2.0](LICENSE).
