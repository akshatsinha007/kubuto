package client

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseChartYaml(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		expectName  string
		expectVer   string
		expectDeps  int
		expectError bool
	}{
		{
			name: "valid YAML with dependencies",
			content: `name: my-app
version: 1.2.3
dependencies:
  - name: argo-cd
    version: 7.3.0
    repository: https://argoproj.github.io/argo-helm
  - name: redis
    version: 18.0.0
    repository: https://charts.bitnami.com/bitnami
`,
			expectName: "my-app",
			expectVer:  "1.2.3",
			expectDeps: 2,
		},
		{
			name: "valid YAML without dependencies",
			content: `name: simple-app
version: 0.1.0
`,
			expectName: "simple-app",
			expectVer:  "0.1.0",
			expectDeps: 0,
		},
		{
			name: "YAML with comments",
			content: `# Chart metadata
name: commented-app
version: 2.0.0
# Dependencies section
dependencies:
  # Main dependency
  - name: postgresql
    version: 14.0.0
    repository: https://charts.bitnami.com/bitnami
`,
			expectName: "commented-app",
			expectVer:  "2.0.0",
			expectDeps: 1,
		},
		{
			name: "YAML with condition and tags",
			content: `name: conditional-app
version: 1.0.0
dependencies:
  - name: redis
    version: 18.0.0
    repository: https://charts.bitnami.com/bitnami
    condition: redis.enabled
    tags:
      - cache
`,
			expectName: "conditional-app",
			expectVer:  "1.0.0",
			expectDeps: 1,
		},
		{
			name: "JSON format (GitHub API response)",
			content: func() string {
				data, _ := json.Marshal(ChartYaml{
					Name:    "json-app",
					Version: "3.0.0",
					Dependencies: []ChartDependency{
						{Name: "nginx", Version: "15.0.0", Repository: "https://charts.bitnami.com/bitnami"},
					},
				})
				return string(data)
			}(),
			expectName: "json-app",
			expectVer:  "3.0.0",
			expectDeps: 1,
		},
		{
			name:        "missing name field",
			content:     `version: 1.0.0`,
			expectError: true,
		},
		{
			name:        "empty content",
			content:     "",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chart, err := parseChartYaml([]byte(tt.content))

			if tt.expectError {
				if err == nil {
					t.Errorf("expected error but got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if chart.Name != tt.expectName {
				t.Errorf("name = %q, want %q", chart.Name, tt.expectName)
			}

			if chart.Version != tt.expectVer {
				t.Errorf("version = %q, want %q", chart.Version, tt.expectVer)
			}

			if len(chart.Dependencies) != tt.expectDeps {
				t.Errorf("dependencies count = %d, want %d", len(chart.Dependencies), tt.expectDeps)
			}

			// Verify first dependency if expected
			if tt.expectDeps > 0 && len(chart.Dependencies) > 0 {
				if chart.Dependencies[0].Name == "" {
					t.Errorf("first dependency has empty name")
				}
				if chart.Dependencies[0].Version == "" {
					t.Errorf("first dependency has empty version")
				}
			}
		})
	}
}

func TestExtractGitHubOwnerRepo(t *testing.T) {
	tests := []struct {
		url      string
		expected string
	}{
		{"https://github.com/argoproj/argo-helm", "argoproj/argo-helm"},
		{"https://github.com/argoproj/argo-helm.git", "argoproj/argo-helm"},
		{"git@github.com:argoproj/argo-helm.git", "argoproj/argo-helm"},
		{"github.com/argoproj/argo-helm", "argoproj/argo-helm"},
		{"https://gitlab.com/some/repo", ""},
		{"not-a-url", ""},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			result := ExtractGitHubOwnerRepo(tt.url)
			if result != tt.expected {
				t.Errorf("extractGitHubOwnerRepo(%q) = %q, want %q", tt.url, result, tt.expected)
			}
		})
	}
}

func TestExtractGitLabProjectPath(t *testing.T) {
	tests := []struct {
		url      string
		expected string
	}{
		{"https://gitlab.com/myorg/myproject", "myorg/myproject"},
		{"https://gitlab.com/myorg/myproject.git", "myorg/myproject"},
		{"https://gitlab.com/group/subgroup/project", "group/subgroup/project"},
		{"https://github.com/owner/repo", ""},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			result := ExtractGitLabProjectPath(tt.url)
			if result != tt.expected {
				t.Errorf("extractGitLabProjectPath(%q) = %q, want %q", tt.url, result, tt.expected)
			}
		})
	}
}

func TestIsGitHub(t *testing.T) {
	tests := []struct {
		url      string
		expected bool
	}{
		{"https://github.com/owner/repo", true},
		{"https://gitlab.com/owner/repo", false},
		{"https://example.com/repo", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			result := IsGitHub(tt.url)
			if result != tt.expected {
				t.Errorf("isGitHub(%q) = %v, want %v", tt.url, result, tt.expected)
			}
		})
	}
}

func TestIsGitLab(t *testing.T) {
	tests := []struct {
		url      string
		expected bool
	}{
		{"https://gitlab.com/owner/repo", true},
		{"https://github.com/owner/repo", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			result := IsGitLab(tt.url)
			if result != tt.expected {
				t.Errorf("isGitLab(%q) = %v, want %v", tt.url, result, tt.expected)
			}
		})
	}
}

func TestFetchChartYaml_GitHub(t *testing.T) {
	// Mock GitHub API response
	chartContent := `name: test-chart
version: 1.0.0
dependencies:
  - name: redis
    version: 18.0.0
    repository: https://charts.bitnami.com/bitnami
`
	encoded := base64.StdEncoding.EncodeToString([]byte(chartContent))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify auth header
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("expected Authorization header with Bearer token")
		}

		// Return mock GitHub API response
		response := map[string]string{
			"content":  encoded,
			"encoding": "base64",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	// Test with mock server (we can't easily override the URL in FetchChartYaml,
	// so we test the parsing logic directly)
	chart, err := parseChartYaml([]byte(chartContent))
	if err != nil {
		t.Fatalf("parseChartYaml failed: %v", err)
	}

	if chart.Name != "test-chart" {
		t.Errorf("name = %q, want %q", chart.Name, "test-chart")
	}

	if chart.Version != "1.0.0" {
		t.Errorf("version = %q, want %q", chart.Version, "1.0.0")
	}

	if len(chart.Dependencies) != 1 {
		t.Fatalf("expected 1 dependency, got %d", len(chart.Dependencies))
	}

	if chart.Dependencies[0].Name != "redis" {
		t.Errorf("dependency name = %q, want %q", chart.Dependencies[0].Name, "redis")
	}
}

func TestNewGitClient_Argocd(t *testing.T) {
	client := NewGitClient("test-token")
	if client == nil {
		t.Fatal("NewGitClient returned nil")
	}

	if client.token != "test-token" {
		t.Errorf("token = %q, want %q", client.token, "test-token")
	}

	if client.httpClient == nil {
		t.Error("httpClient is nil")
	}

	if client.httpClient.Timeout == 0 {
		t.Error("httpClient timeout is 0")
	}
}

func TestNewGitClient_NoToken(t *testing.T) {
	client := NewGitClient("")
	if client == nil {
		t.Fatal("NewGitClient returned nil")
	}

	if client.token != "" {
		t.Errorf("token = %q, want empty", client.token)
	}
}

func TestFetchChartYaml_Context(t *testing.T) {
	client := NewGitClient("")

	// Test with cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := client.FetchChartYaml(ctx, "https://github.com/owner/repo", "", "main")
	if err == nil {
		t.Error("expected error with cancelled context")
	}
}
