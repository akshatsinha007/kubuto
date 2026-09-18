package client

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// GitClient fetches Chart.yaml from git repositories.
type GitClient struct {
	token      string
	httpClient *http.Client
}

// GitAuth carries per-repo credentials for a single Chart.yaml fetch.
// It is intentionally small: the only auth flavours kubuto inherits
// from ArgoCD repo-secrets and can use over plain HTTPS are
//   - basic auth (Username + Password) — covers GitHub PAT/GitLab deploy
//     token/Bitbucket app password since each provider treats the PAT as
//     the password and the literal string "x-token-auth" / username as
//     the user. We always send Basic when both are set, so vendor-
//     specific header quirks (`token <pat>` vs `Bearer <pat>`) are
//     handled by the providers' Basic-auth fallback path.
//   - bearer (Token) — used as `Authorization: token <T>` for GitHub
//     contents API and `PRIVATE-TOKEN: <T>` for GitLab API. This matches
//     `NewGitClient(token).FetchChartYaml(...)` (the legacy path) so
//     existing tests continue to pass with `GitAuth{Token: ...}`.
//
// Anything else (SSH private keys, GitHub App private keys, mTLS) is
// out of scope for the kubuto-scan path; ArgoCD itself can still sync
// those repos, but kubuto reports them as "git@<shortSHA>" with a clear
// fallback message and lets the user supply HTTPS creds via
// `scanning.gitops` if they want resolved versions.
type GitAuth struct {
	Username string
	Password string
	Token    string
}

// IsZero reports whether auth carries no usable credentials. The
// fetcher uses this to decide whether to send headers at all — we want
// public repos to keep working without spurious "Authorization:" leaks.
func (a GitAuth) IsZero() bool {
	return a.Username == "" && a.Password == "" && a.Token == ""
}

// NewGitClient creates a new GitClient.
//
// `token` is preserved on the receiver for backwards compatibility with
// the `FetchChartYaml(...)` legacy entrypoint and the tests that use it
// (`internal/client/git_test.go::TestFetchChartYaml_NoAuth` etc.).
// New callers should use `FetchChartYamlWithAuth(...)` and pass per-call
// credentials so the same client instance can serve many repos.
func NewGitClient(token string) *GitClient {
	return &GitClient{
		token: token,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// ChartYaml represents a parsed Chart.yaml file.
type ChartYaml struct {
	Name         string            `yaml:"name" json:"name"`
	Version      string            `yaml:"version" json:"version"`
	Dependencies []ChartDependency `yaml:"dependencies" json:"dependencies"`
}

// ChartDependency represents a dependency in Chart.yaml.
type ChartDependency struct {
	Name       string `yaml:"name" json:"name"`
	Version    string `yaml:"version" json:"version"`
	Repository string `yaml:"repository" json:"repository"`
	Condition  string `yaml:"condition" json:"condition,omitempty"`
	Tags       string `yaml:"tags" json:"tags,omitempty"`
}

// FetchChartYaml fetches Chart.yaml using the credentials baked into
// the client at construction time (the legacy `NewGitClient(token)`
// shape). New callers should prefer FetchChartYamlWithAuth so a single
// long-lived client can serve repos with different credentials.
func (g *GitClient) FetchChartYaml(ctx context.Context, repo, path, branch string) (*ChartYaml, error) {
	return g.FetchChartYamlWithAuth(ctx, repo, path, branch, GitAuth{Token: g.token})
}

// FetchChartYamlWithAuth fetches Chart.yaml from a git repository using
// the supplied per-call credentials. An empty `auth` is allowed and
// produces an unauthenticated request — public repos must keep working
// without spurious "Authorization:" leaks. Supports GitHub, GitLab, and
// generic Git servers (raw HTTPS).
//
// The caller decides which auth flavour to populate:
//   - `Token` is treated as a bearer/private token (matches the legacy
//     `FetchChartYaml` behaviour: `Authorization: token <T>` for
//     GitHub, `PRIVATE-TOKEN: <T>` for GitLab, `Authorization: token`
//     for raw URLs).
//   - `Username` + `Password` is sent as HTTP Basic — the universal
//     fallback that works for GitHub PATs ("x-token-auth" or any
//     non-empty user + PAT as password), GitLab deploy tokens,
//     Bitbucket app passwords, Azure DevOps PATs, etc.
//
// Mixing both is allowed: Basic wins because it is more specific
// (callers explicitly populated it). This matches what `git` itself
// does when both are present in `.git-credentials`.
func (g *GitClient) FetchChartYamlWithAuth(ctx context.Context, repo, path, branch string, auth GitAuth) (*ChartYaml, error) {
	if branch == "" {
		branch = "main"
	}

	// Normalize path
	path = strings.TrimPrefix(path, "/")
	path = strings.TrimSuffix(path, "/")

	// Try GitHub API first
	if IsGitHub(repo) {
		return g.fetchFromGitHubWithAuth(ctx, repo, path, branch, auth)
	}

	// Try GitLab API
	if IsGitLab(repo) {
		return g.fetchFromGitLabWithAuth(ctx, repo, path, branch, auth)
	}

	// Fallback: raw URL
	rawURL := g.buildRawURL(repo, path, branch)
	if rawURL == "" {
		return nil, fmt.Errorf("cannot determine raw URL for repo: %s", repo)
	}
	return g.fetchRawWithAuth(ctx, rawURL, auth)
}

// applyAuth attaches the appropriate auth header to a request.
//
// Vendor flag controls the bearer-token flavour:
//   - "github" → `Authorization: token <T>` (Contents API)
//   - "gitlab" → `PRIVATE-TOKEN: <T>`        (REST API)
//   - ""/other → `Authorization: token <T>`  (raw, generic)
//
// Basic auth (Username + Password) always wins when populated because
// callers who set both did so deliberately (e.g. mirror creds from an
// ArgoCD `repository` secret with both fields).
func applyAuth(req *http.Request, auth GitAuth, vendor string) {
	if auth.IsZero() {
		return
	}
	if auth.Username != "" || auth.Password != "" {
		// http.Request.SetBasicAuth handles base64 + escaping correctly.
		req.SetBasicAuth(auth.Username, auth.Password)
		return
	}
	switch vendor {
	case "gitlab":
		req.Header.Set("PRIVATE-TOKEN", auth.Token)
	default:
		req.Header.Set("Authorization", "token "+auth.Token)
	}
}

// buildRawURL constructs a raw URL for fetching Chart.yaml from a repository.
func (g *GitClient) buildRawURL(repo, path, branch string) string {
	if branch == "" {
		branch = "main"
	}

	path = strings.TrimPrefix(path, "/")
	path = strings.TrimSuffix(path, "/")

	chartPath := path
	if chartPath != "" {
		chartPath = chartPath + "/"
	}
	chartPath = chartPath + "Chart.yaml"

	repo = strings.TrimSuffix(repo, ".git")
	repo = strings.TrimSuffix(repo, "/")

	switch {
	case strings.Contains(repo, "github.com"):
		parts := strings.Split(strings.TrimPrefix(repo, "https://github.com/"), "/")
		if len(parts) >= 2 {
			return fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s/%s",
				parts[0], parts[1], branch, chartPath)
		}
	case strings.Contains(repo, "gitlab.com"):
		return fmt.Sprintf("%s/-/raw/%s/%s", repo, branch, chartPath)
	default:
		// Self-hosted GitLab or generic
		return fmt.Sprintf("%s/-/raw/%s/%s", repo, branch, chartPath)
	}
	return ""
}

// fetchRaw is a backwards-compat shim that preserves the original
// signature used by `internal/client/git_test.go`. It forwards to
// fetchRawWithAuth using the receiver's baked-in token (the legacy
// shape). Production callers go through FetchChartYamlWithAuth.
func (g *GitClient) fetchRaw(ctx context.Context, rawURL string) (*ChartYaml, error) {
	return g.fetchRawWithAuth(ctx, rawURL, GitAuth{Token: g.token})
}

// fetchRawWithAuth fetches and parses Chart.yaml from a raw URL,
// honouring the supplied per-call credentials. The legacy signed-on-the-
// receiver token is no longer consulted here — callers that want it
// must pass it via auth.Token (which `FetchChartYaml` does).
func (g *GitClient) fetchRawWithAuth(ctx context.Context, rawURL string, auth GitAuth) (*ChartYaml, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	applyAuth(req, auth, "")

	resp, err := g.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("raw URL request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("raw URL returned %d: %s", resp.StatusCode, string(body))
	}

	content, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	return parseChartYaml(content)
}

// fetchFromGitHubWithAuth fetches Chart.yaml using GitHub Contents API.
func (g *GitClient) fetchFromGitHubWithAuth(ctx context.Context, repo, path, branch string, auth GitAuth) (*ChartYaml, error) {
	ownerRepo := ExtractGitHubOwnerRepo(repo)
	if ownerRepo == "" {
		return nil, fmt.Errorf("invalid GitHub repo URL: %s", repo)
	}

	chartPath := path
	if chartPath != "" {
		chartPath = chartPath + "/"
	}
	chartPath = chartPath + "Chart.yaml"

	url := fmt.Sprintf("https://api.github.com/repos/%s/contents/%s?ref=%s", ownerRepo, chartPath, branch)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Accept", "application/vnd.github.v3+json")
	applyAuth(req, auth, "github")

	resp, err := g.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GitHub API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GitHub API returned %d: %s", resp.StatusCode, string(body))
	}

	var ghResp struct {
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&ghResp); err != nil {
		return nil, fmt.Errorf("failed to decode GitHub response: %w", err)
	}

	if ghResp.Encoding != "base64" {
		return nil, fmt.Errorf("unexpected encoding: %s", ghResp.Encoding)
	}

	content, err := base64.StdEncoding.DecodeString(ghResp.Content)
	if err != nil {
		return nil, fmt.Errorf("failed to decode base64 content: %w", err)
	}

	return parseChartYaml(content)
}

// fetchFromGitLabWithAuth fetches Chart.yaml using GitLab API.
func (g *GitClient) fetchFromGitLabWithAuth(ctx context.Context, repo, path, branch string, auth GitAuth) (*ChartYaml, error) {
	projectPath := ExtractGitLabProjectPath(repo)
	if projectPath == "" {
		return nil, fmt.Errorf("invalid GitLab repo URL: %s", repo)
	}

	chartPath := path
	if chartPath != "" {
		chartPath = chartPath + "/"
	}
	chartPath = chartPath + "Chart.yaml"

	encodedProject := strings.ReplaceAll(projectPath, "/", "%2F")
	url := fmt.Sprintf("https://gitlab.com/api/v4/projects/%s/repository/files/%s?ref=%s",
		encodedProject, strings.ReplaceAll(chartPath, "/", "%2F"), branch)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	applyAuth(req, auth, "gitlab")

	resp, err := g.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GitLab API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GitLab API returned %d: %s", resp.StatusCode, string(body))
	}

	var glResp struct {
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&glResp); err != nil {
		return nil, fmt.Errorf("failed to decode GitLab response: %w", err)
	}

	if glResp.Encoding != "base64" {
		return nil, fmt.Errorf("unexpected encoding: %s", glResp.Encoding)
	}

	content, err := base64.StdEncoding.DecodeString(glResp.Content)
	if err != nil {
		return nil, fmt.Errorf("failed to decode base64 content: %w", err)
	}

	return parseChartYaml(content)
}

// parseChartYaml parses Chart.yaml content.
func parseChartYaml(content []byte) (*ChartYaml, error) {
	// Try JSON first (for GitHub API responses)
	var chart ChartYaml
	if err := json.Unmarshal(content, &chart); err == nil && chart.Name != "" {
		return &chart, nil
	}

	// Parse YAML manually (simple key-value + dependencies list)
	chart = ChartYaml{}
	lines := strings.Split(string(content), "\n")
	inDeps := false
	depIndent := -1
	var currentDep *ChartDependency

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Skip comments and empty lines
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		// Check for dependencies section
		if strings.HasPrefix(trimmed, "dependencies:") {
			inDeps = true
			continue
		}

		if inDeps {
			// Check if we're still in dependencies (indented)
			indent := len(line) - len(strings.TrimLeft(line, " "))
			if indent == 0 {
				inDeps = false
				depIndent = -1
				continue
			}

			// New dependency entry (only at the expected indent level)
			if strings.HasPrefix(trimmed, "- ") {
				if depIndent == -1 {
					depIndent = indent
				}
				if indent == depIndent {
					if currentDep != nil {
						chart.Dependencies = append(chart.Dependencies, *currentDep)
					}
					currentDep = &ChartDependency{}
					trimmed = strings.TrimPrefix(trimmed, "- ")
				} else {
					// Nested list item (e.g., tags: - cache), skip
					continue
				}
			}

			if currentDep != nil {
				parts := strings.SplitN(trimmed, ":", 2)
				if len(parts) == 2 {
					key := strings.TrimSpace(parts[0])
					value := strings.TrimSpace(parts[1])
					value = strings.Trim(value, "\"'")

					switch key {
					case "name":
						currentDep.Name = value
					case "version":
						currentDep.Version = value
					case "repository":
						currentDep.Repository = value
					case "condition":
						currentDep.Condition = value
					case "tags":
						currentDep.Tags = value
					}
				}
			}
			continue
		}

		// Top-level fields
		parts := strings.SplitN(trimmed, ":", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])
			value = strings.Trim(value, "\"'")

			switch key {
			case "name":
				chart.Name = value
			case "version":
				chart.Version = value
			}
		}
	}

	// Add last dependency
	if currentDep != nil {
		chart.Dependencies = append(chart.Dependencies, *currentDep)
	}

	if chart.Name == "" {
		return nil, fmt.Errorf("failed to parse Chart.yaml: missing name field")
	}

	return &chart, nil
}
