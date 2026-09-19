package scanner

import (
	"errors"
	"strings"
	"testing"

	"github.com/akshatsinha007/kubuto/internal/client"
	"github.com/akshatsinha007/kubuto/internal/config"
)

// TestResolveGitAuth_PreferenceOrder asserts the top of the preference
// chain documented on `resolveGitAuth`: exact-repository secret beats
// repo-creds prefix beats opts.GitOpsConfigs override beats anonymous.
// (The DefaultGitAuth tier between GitOpsConfigs and anonymous is
// covered separately by TestResolveGitAuth_DefaultGitAuthIsLastResort.)
// Each row keeps the secret universe identical so we're isolating the
// one knob that shifts the resolution tier.
func TestResolveGitAuth_PreferenceOrder(t *testing.T) {
	scanner := &ArgoCDScanner{}

	repoURL := "https://github.com/acme/devtron-gitops"

	exactSecret := repoSecret{
		Name: "exact", URL: repoURL, Type: "git",
		Username: "exact-user", Password: "exact-pat",
	}
	credsSecret := repoSecret{
		Name: "creds", URL: "https://github.com/acme", Type: "git",
		IsCreds: true, Username: "prefix-user", Password: "prefix-pat",
	}
	helmSecret := repoSecret{
		// repo-secrets with type=helm must never leak into git auth
		// resolution — they belong to the chart-repo path.
		Name: "ignored-helm", URL: repoURL, Type: "helm",
		Username: "helm-user", Password: "helm-pat",
	}

	cases := []struct {
		name    string
		secrets []repoSecret
		opts    ScanOptions
		want    client.GitAuth
	}{
		{
			name:    "exact match wins over everything",
			secrets: []repoSecret{credsSecret, exactSecret, helmSecret},
			opts: ScanOptions{GitOpsConfigs: []config.GitOpsRepo{
				{Repo: repoURL, Token: "override-token"},
			}},
			want: client.GitAuth{Username: "exact-user", Password: "exact-pat"},
		},
		{
			name:    "longest-prefix repo-creds beats opts override",
			secrets: []repoSecret{credsSecret, helmSecret},
			opts: ScanOptions{GitOpsConfigs: []config.GitOpsRepo{
				{Repo: repoURL, Token: "override-token"},
			}},
			want: client.GitAuth{Username: "prefix-user", Password: "prefix-pat"},
		},
		{
			name:    "opts override beats anonymous when no secrets match",
			secrets: []repoSecret{helmSecret},
			opts: ScanOptions{GitOpsConfigs: []config.GitOpsRepo{
				{Repo: repoURL, Token: "override-token"},
			}},
			want: client.GitAuth{Token: "override-token"},
		},
		{
			name:    "anonymous when nothing matches",
			secrets: []repoSecret{helmSecret},
			opts:    ScanOptions{},
			want:    client.GitAuth{},
		},
		{
			name: "password-only secret defaults username to x-token-auth",
			secrets: []repoSecret{{
				Name: "pw-only", URL: repoURL, Type: "git",
				Password: "ghp_XXX",
			}},
			opts: ScanOptions{},
			want: client.GitAuth{Username: "x-token-auth", Password: "ghp_XXX"},
		},
		{
			name: "ssh-only secret skipped, falls through to anonymous",
			secrets: []repoSecret{{
				Name: "ssh-only", URL: repoURL, Type: "git",
				// no Username/Password populated — kubuto treats it as unusable
			}},
			opts: ScanOptions{},
			want: client.GitAuth{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := scanner.resolveGitAuth(repoURL, tc.secrets, tc.opts)
			if got != tc.want {
				t.Fatalf("resolveGitAuth() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestResolveGitAuth_LongestPrefixWins guards the "longest prefix" rule
// for repo-creds: a generic `https://github.com/acme` template should
// lose to a more specific `https://github.com/acme/critical-service`
// when both could match.
func TestResolveGitAuth_LongestPrefixWins(t *testing.T) {
	scanner := &ArgoCDScanner{}
	repoURL := "https://github.com/acme/critical-service"

	short := repoSecret{
		Name: "short", URL: "https://github.com/acme", Type: "git",
		IsCreds: true, Username: "short-user", Password: "short-pat",
	}
	long := repoSecret{
		Name: "long", URL: "https://github.com/acme/critical-service",
		Type: "git", IsCreds: true,
		Username: "long-user", Password: "long-pat",
	}

	got := scanner.resolveGitAuth(repoURL, []repoSecret{short, long}, ScanOptions{})
	want := client.GitAuth{Username: "long-user", Password: "long-pat"}
	if got != want {
		t.Fatalf("longest prefix did not win: got %+v want %+v", got, want)
	}
}

// TestResolveGitAuth_ManualOverridePrefixMatch guards the real-world
// case this tier exists for: a GitOps setup with hundreds/thousands of
// per-app repos under one host/group, none of whose ArgoCD secrets
// carry usable credentials, but where a single PAT is valid for every
// repo under that group. One `scanning.argocd.gitops` entry naming the
// group must resolve every repo nested under it, not just an
// exact-string match.
func TestResolveGitAuth_ManualOverridePrefixMatch(t *testing.T) {
	scanner := &ArgoCDScanner{}
	repoURL := "https://gitlab.com/devtron-prod-gitops/prometheus-rule-central.git"

	opts := ScanOptions{GitOpsConfigs: []config.GitOpsRepo{
		{Repo: "https://gitlab.com/devtron-prod-gitops", Token: "group-pat"},
	}}

	got := scanner.resolveGitAuth(repoURL, nil, opts)
	want := client.GitAuth{Token: "group-pat"}
	if got != want {
		t.Fatalf("group-prefix override did not match nested repo: got %+v want %+v", got, want)
	}
}

// TestResolveGitAuth_ManualOverrideUsernamePassword covers the
// Username/Password shape of a manual override (not every override is
// a bearer token — GitLab deploy tokens, Bitbucket app passwords, etc.
// use Basic auth), and that it's only used when Token is empty.
func TestResolveGitAuth_ManualOverrideUsernamePassword(t *testing.T) {
	scanner := &ArgoCDScanner{}
	repoURL := "https://gitlab.com/acme/svc"

	basicOnly := ScanOptions{GitOpsConfigs: []config.GitOpsRepo{
		{Repo: repoURL, Username: "deploy-token-user", Password: "deploy-token-pw"},
	}}
	got := scanner.resolveGitAuth(repoURL, nil, basicOnly)
	want := client.GitAuth{Username: "deploy-token-user", Password: "deploy-token-pw"}
	if got != want {
		t.Fatalf("username/password override not applied: got %+v want %+v", got, want)
	}

	tokenWins := ScanOptions{GitOpsConfigs: []config.GitOpsRepo{
		{Repo: repoURL, Token: "the-token", Username: "ignored", Password: "ignored"},
	}}
	got = scanner.resolveGitAuth(repoURL, nil, tokenWins)
	want = client.GitAuth{Token: "the-token"}
	if got != want {
		t.Fatalf("token should win over username/password when both set: got %+v want %+v", got, want)
	}
}

// TestResolveGitAuth_ManualOverrideLongestPrefixWins mirrors the
// repo-creds longest-prefix guarantee for the manual-override tier: a
// more specific entry must win over a broader group-level one.
func TestResolveGitAuth_ManualOverrideLongestPrefixWins(t *testing.T) {
	scanner := &ArgoCDScanner{}
	repoURL := "https://gitlab.com/acme/critical-service.git"

	opts := ScanOptions{GitOpsConfigs: []config.GitOpsRepo{
		{Repo: "https://gitlab.com/acme", Token: "group-pat"},
		{Repo: "https://gitlab.com/acme/critical-service", Token: "specific-pat"},
	}}

	got := scanner.resolveGitAuth(repoURL, nil, opts)
	want := client.GitAuth{Token: "specific-pat"}
	if got != want {
		t.Fatalf("more specific override should win: got %+v want %+v", got, want)
	}
}

// TestResolveGitAuth_DefaultGitAuthIsLastResort asserts opts.DefaultGitAuth
// (--git-token/--git-username/--git-password) only kicks in once every
// secret-based and GitOpsConfigs-based tier has missed, and that it
// still loses to a matching GitOpsConfigs entry when one exists.
func TestResolveGitAuth_DefaultGitAuthIsLastResort(t *testing.T) {
	scanner := &ArgoCDScanner{}
	repoURL := "https://gitlab.com/acme/svc.git"

	// No secrets, no GitOpsConfigs match → DefaultGitAuth wins over anonymous.
	got := scanner.resolveGitAuth(repoURL, nil, ScanOptions{
		DefaultGitAuth: client.GitAuth{Token: "global-fallback"},
	})
	want := client.GitAuth{Token: "global-fallback"}
	if got != want {
		t.Fatalf("DefaultGitAuth should win over anonymous: got %+v want %+v", got, want)
	}

	// A matching GitOpsConfigs entry still beats DefaultGitAuth.
	got = scanner.resolveGitAuth(repoURL, nil, ScanOptions{
		DefaultGitAuth: client.GitAuth{Token: "global-fallback"},
		GitOpsConfigs:  []config.GitOpsRepo{{Repo: repoURL, Token: "specific-override"}},
	})
	want = client.GitAuth{Token: "specific-override"}
	if got != want {
		t.Fatalf("specific GitOpsConfigs entry should beat DefaultGitAuth: got %+v want %+v", got, want)
	}
}

// TestParseRepoSecret_GitAndHelm pins the new behaviour: git secrets
// are kept (they used to be silently dropped) and credential fields are
// parsed for both flavours.
func TestParseRepoSecret_GitAndHelm(t *testing.T) {
	gitData := map[string][]byte{
		"url":      []byte("https://github.com/acme/foo"),
		"type":     []byte("git"),
		"username": []byte("alice"),
		"password": []byte("p@ss"),
	}
	rs := parseRepoSecret("argocd-git-secret", gitData, false)
	if rs.Type != "git" || rs.Username != "alice" || rs.Password != "p@ss" {
		t.Fatalf("git secret parsed wrong: %+v", rs)
	}
	if rs.IsCreds {
		t.Fatalf("non-creds secret marked as creds")
	}
	if rs.auth().IsZero() {
		t.Fatalf("auth() should be non-zero for populated git secret")
	}

	helmData := map[string][]byte{
		"url":      []byte("https://charts.example.com"),
		"type":     []byte("helm"),
		"username": []byte("u"),
		"password": []byte("p"),
	}
	hrs := parseRepoSecret("helm-secret", helmData, false)
	if hrs.Type != "helm" {
		t.Fatalf("helm secret type lost")
	}

	credsData := map[string][]byte{
		"url":      []byte("https://github.com/acme"),
		"type":     []byte("git"),
		"username": []byte("c"),
		"password": []byte("p"),
	}
	crs := parseRepoSecret("creds", credsData, true)
	if !crs.IsCreds {
		t.Fatalf("creds flag not propagated")
	}
}

// TestGitRevisionLabel covers the three-way branching: empty
// targetRevision, full SHA (truncated), short ref / branch (kept as-is).
func TestGitRevisionLabel(t *testing.T) {
	cases := map[string]string{
		"":       "git@unknown",
		"  ":     "git@unknown",
		"main":   "git@main",
		"v1.2.3": "git@v1.2.3",
		"abcdef0123456789abcdef0123456789abcdef01": "git@abcdef0",
	}
	for in, want := range cases {
		if got := gitRevisionLabel(in); got != want {
			t.Errorf("gitRevisionLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestSanitiseFetchError redacts secrets and trims the error to one
// line so the table column never contains a multi-line server body.
func TestSanitiseFetchError(t *testing.T) {
	auth := client.GitAuth{Token: "ghp_supersecret", Password: "p@ss"}
	err := errors.New("GitHub API returned 401: token=ghp_supersecret password=p@ss\nResponse-Body: <html>")

	got := sanitiseFetchError(err, auth)

	if strings.Contains(got, "ghp_supersecret") || strings.Contains(got, "p@ss") {
		t.Fatalf("sanitiseFetchError leaked credentials: %q", got)
	}
	if strings.Contains(got, "\n") {
		t.Fatalf("sanitiseFetchError did not strip newline: %q", got)
	}
	if !strings.Contains(got, "GitHub API returned 401") {
		t.Fatalf("sanitiseFetchError dropped the useful prefix: %q", got)
	}
}

// TestLooksLikeSHA covers the heuristic boundary cases. We only treat
// the obvious 7-40-char hex strings as SHAs; everything else is a
// branch/tag and goes to the fetcher unchanged.
func TestLooksLikeSHA(t *testing.T) {
	yes := []string{
		"abcdef0",
		"0123456789abcdef0123456789abcdef01234567",
	}
	no := []string{
		"",
		"main",
		"v1.2.3",
		"abcdefg", // 'g' is not hex
		"abcdef01234567890abcdef01234567890abcdef0", // 41 chars
		"ABCDEF0", // uppercase rejected
	}
	for _, s := range yes {
		if !looksLikeSHA(s) {
			t.Errorf("looksLikeSHA(%q) = false, want true", s)
		}
	}
	for _, s := range no {
		if looksLikeSHA(s) {
			t.Errorf("looksLikeSHA(%q) = true, want false", s)
		}
	}
}

// TestResolveGitBranch verifies precedence: an Application's own
// targetRevision wins over kubuto config; SHAs fall through.
func TestResolveGitBranch(t *testing.T) {
	scanner := &ArgoCDScanner{}
	repoURL := "https://github.com/acme/foo"

	if got := scanner.resolveGitBranch(repoURL, ScanOptions{}, "release-2.1"); got != "release-2.1" {
		t.Fatalf("targetRevision branch should win, got %q", got)
	}
	// SHA falls through to opts override
	opts := ScanOptions{GitOpsConfigs: []config.GitOpsRepo{
		{Repo: repoURL, Branch: "main"},
	}}
	if got := scanner.resolveGitBranch(repoURL, opts, "abcdef0"); got != "main" {
		t.Fatalf("SHA targetRevision should fall through to opts branch, got %q", got)
	}
	// Empty everything → empty string (FetchChartYamlWithAuth defaults to "main")
	if got := scanner.resolveGitBranch(repoURL, ScanOptions{}, ""); got != "" {
		t.Fatalf("empty input should produce empty string, got %q", got)
	}
}
