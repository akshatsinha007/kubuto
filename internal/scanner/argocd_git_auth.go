package scanner

import (
	"strings"

	"github.com/akshatsinha007/kubuto/internal/client"
	"github.com/akshatsinha007/kubuto/internal/config"
)

// resolveGitAuth picks the best HTTPS credentials available for `repoURL`
// and returns them as a `client.GitAuth` ready for
// `FetchChartYamlWithAuth`. The preference order is:
//
//  1. Exact-match `secret-type=repository` git secret with HTTPS creds.
//     (User explicitly bound creds to *this* repo in ArgoCD.)
//  2. Longest-prefix `secret-type=repo-creds` template with HTTPS creds.
//     (User said "every repo under this prefix uses these creds".)
//  3. Longest-prefix match against `opts.GitOpsConfigs[]` (the
//     `scanning.argocd.gitops` config knob, or `Repo`/`URL` set
//     manually) — kubuto's manual override, useful for repos ArgoCD
//     itself doesn't track, or where the ArgoCD secret it does have
//     carries no usable credential (common: Devtron/ArgoCD registers
//     one `repository` secret per generated app repo but the actual
//     auth lives outside ArgoCD entirely, e.g. injected by an
//     orchestrator at a different layer). Matched by prefix, same rule
//     as pass 2, so one entry can cover an entire git host/group
//     instead of requiring one entry per repo.
//  4. `opts.DefaultGitAuth` — a single global credential from
//     `--git-token`/`--git-username`/`--git-password` (or their config
//     equivalents). Absolute last resort before anonymous: covers repos
//     with no secret and no per-repo/per-prefix override at all.
//  5. Anonymous (zero-value GitAuth) — public repos work without any
//     header. The HTTP layer leaves `Authorization` unset.
//
// SSH-only / GitHub-App-only secrets are deliberately ignored: kubuto's
// fetch path is HTTPS, and forwarding those credentials would only
// invite confused error reports. The caller upstream (`processGitSource`)
// degrades gracefully into the `git@<sha>` fallback row when an HTTPS
// fetch fails.
func (s *ArgoCDScanner) resolveGitAuth(repoURL string, secrets []repoSecret, opts ScanOptions) client.GitAuth {
	if repoURL == "" {
		return client.GitAuth{}
	}

	normalizedURL := client.NormalizeRepoURL(repoURL)

	// Pass 1: exact `secret-type=repository` match with HTTPS creds.
	// We require both username AND password populated; ArgoCD allows
	// password-only auth (treated as "send PAT in password slot, set
	// any non-empty username") so we mirror that by defaulting the
	// username to "x-token-auth" when only password is present.
	for _, sec := range secrets {
		if sec.IsCreds || sec.Type == "helm" {
			continue
		}
		if sec.URL == "" {
			continue
		}
		if client.NormalizeRepoURL(sec.URL) != normalizedURL {
			continue
		}
		if a := credsFromSecret(sec); !a.IsZero() {
			return a
		}
	}

	// Pass 2: longest-prefix `secret-type=repo-creds` match.
	bestPrefixLen := -1
	var bestCreds client.GitAuth
	for _, sec := range secrets {
		if !sec.IsCreds {
			continue
		}
		if sec.URL == "" {
			continue
		}
		normalizedSecret := client.NormalizeRepoURL(sec.URL)
		// We want `normalizedSecret` to be a strict path-ancestor of
		// `normalizedURL` (or equal).
		if normalizedSecret != normalizedURL && !hasURLPrefix(normalizedURL, normalizedSecret) {
			continue
		}
		if len(normalizedSecret) <= bestPrefixLen {
			continue
		}
		if a := credsFromSecret(sec); !a.IsZero() {
			bestPrefixLen = len(normalizedSecret)
			bestCreds = a
		}
	}
	if bestPrefixLen >= 0 {
		return bestCreds
	}

	// Pass 3: longest-prefix match against `scanning.gitops` overrides.
	bestPrefixLen = -1
	var bestManual client.GitAuth
	for _, gc := range opts.GitOpsConfigs {
		matchKey := gc.Repo
		if matchKey == "" {
			matchKey = gc.URL
		}
		if matchKey == "" {
			continue
		}
		normalizedGC := client.NormalizeRepoURL(matchKey)
		if normalizedGC != normalizedURL && !hasURLPrefix(normalizedURL, normalizedGC) {
			continue
		}
		if len(normalizedGC) <= bestPrefixLen {
			continue
		}
		if a := credsFromGitOpsConfig(gc); !a.IsZero() {
			bestPrefixLen = len(normalizedGC)
			bestManual = a
		}
	}
	if bestPrefixLen >= 0 {
		return bestManual
	}

	// Pass 4: global default (--git-token / --git-username / --git-password).
	if !opts.DefaultGitAuth.IsZero() {
		return opts.DefaultGitAuth
	}

	return client.GitAuth{}
}

// credsFromGitOpsConfig materialises HTTPS credentials from a manual
// `scanning.gitops` entry. Token takes precedence when both a token
// and username/password are set — it's the more common shape for this
// config knob (a single PAT), and picking one deterministic winner
// beats silently combining them.
func credsFromGitOpsConfig(gc config.GitOpsRepo) client.GitAuth {
	if gc.Token != "" {
		return client.GitAuth{Token: gc.Token}
	}
	if gc.Username != "" || gc.Password != "" {
		return client.GitAuth{Username: gc.Username, Password: gc.Password}
	}
	return client.GitAuth{}
}

// credsFromSecret materialises an HTTPS credential bundle from an
// ArgoCD secret payload, filling in the well-known "x-token-auth"
// username when only a password (PAT) is set so the resulting Basic
// auth header is universally accepted (GitHub, GitLab, Bitbucket).
func credsFromSecret(sec repoSecret) client.GitAuth {
	if sec.Username == "" && sec.Password == "" {
		return client.GitAuth{}
	}
	user := sec.Username
	if user == "" {
		user = "x-token-auth"
	}
	return client.GitAuth{Username: user, Password: sec.Password}
}

// resolveGitBranch picks the branch we'll fetch Chart.yaml from. The
// most authoritative source is the Application's own `targetRevision`
// (HEAD-style refs and branch names alike); we only fall back to the
// kubuto config or "main" when targetRevision is missing or clearly a
// commit SHA (which `FetchChartYamlWithAuth` can't use as a branch).
func (s *ArgoCDScanner) resolveGitBranch(repoURL string, opts ScanOptions, targetRevision string) string {
	if targetRevision != "" && !looksLikeSHA(targetRevision) {
		return targetRevision
	}
	for _, gc := range opts.GitOpsConfigs {
		if gc.Branch == "" {
			continue
		}
		if gc.Repo == "" || client.ReposMatch(gc.Repo, repoURL) {
			return gc.Branch
		}
	}
	return ""
}

// looksLikeSHA returns true when `s` is plausibly a git commit SHA —
// 7-40 lowercase hex characters. Used to avoid asking GitHub's contents
// API for `?ref=<sha>` via the branch parameter (which would 404).
func looksLikeSHA(s string) bool {
	if len(s) < 7 || len(s) > 40 {
		return false
	}
	for _, r := range s {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return false
		}
	}
	return true
}

// gitRevisionLabel renders the user-facing "we couldn't resolve a chart
// version, here's the git revision instead" string. Falls back to
// `git@unknown` when the Application has no targetRevision at all
// (HEAD), so the table cell never goes blank.
func gitRevisionLabel(targetRevision string) string {
	rev := strings.TrimSpace(targetRevision)
	if rev == "" {
		return "git@unknown"
	}
	if looksLikeSHA(rev) && len(rev) > 7 {
		rev = rev[:7]
	}
	return "git@" + rev
}

// sanitiseFetchError strips credentials from a fetch error before it
// lands in the Resource.CompatError field. Errors from the http layer
// can include the request URL (which a misconfigured user might have
// stuffed creds into) — we redact any occurrence of the auth strings
// we know about so logs and table output stay safe to share.
func sanitiseFetchError(err error, auth client.GitAuth) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if auth.Password != "" {
		msg = strings.ReplaceAll(msg, auth.Password, "***")
	}
	if auth.Token != "" {
		msg = strings.ReplaceAll(msg, auth.Token, "***")
	}
	// Trim noisy multi-line response bodies; the first line is plenty
	// for a CompatError column.
	if i := strings.IndexByte(msg, '\n'); i >= 0 {
		msg = msg[:i]
	}
	const max = 200
	if len(msg) > max {
		msg = msg[:max] + "…"
	}
	return msg
}
