package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewGitClient(t *testing.T) {
	client := NewGitClient("test-token")
	if client == nil {
		t.Fatal("NewGitClient returned nil")
	}
	if client.token != "test-token" {
		t.Errorf("expected token 'test-token', got '%s'", client.token)
	}
	if client.httpClient == nil {
		t.Error("httpClient is nil")
	}
}

func TestNewGitClientEmptyToken(t *testing.T) {
	client := NewGitClient("")
	if client == nil {
		t.Fatal("NewGitClient returned nil")
	}
	if client.token != "" {
		t.Errorf("expected empty token, got '%s'", client.token)
	}
}

func TestBuildRawURL_GitHub(t *testing.T) {
	client := NewGitClient("")

	tests := []struct {
		name      string
		repoURL   string
		chartPath string
		branch    string
		expected  string
	}{
		{
			name:      "github root chart",
			repoURL:   "https://github.com/org/repo",
			chartPath: "",
			branch:    "main",
			expected:  "https://raw.githubusercontent.com/org/repo/main/Chart.yaml",
		},
		{
			name:      "github with path",
			repoURL:   "https://github.com/org/repo",
			chartPath: "charts/myapp",
			branch:    "main",
			expected:  "https://raw.githubusercontent.com/org/repo/main/charts/myapp/Chart.yaml",
		},
		{
			name:      "github with .git suffix",
			repoURL:   "https://github.com/org/repo.git",
			chartPath: "",
			branch:    "develop",
			expected:  "https://raw.githubusercontent.com/org/repo/develop/Chart.yaml",
		},
		{
			name:      "github with trailing slash",
			repoURL:   "https://github.com/org/repo/",
			chartPath: "deploy/chart",
			branch:    "v2",
			expected:  "https://raw.githubusercontent.com/org/repo/v2/deploy/chart/Chart.yaml",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := client.buildRawURL(tt.repoURL, tt.chartPath, tt.branch)
			if got != tt.expected {
				t.Errorf("buildRawURL() = %s, want %s", got, tt.expected)
			}
		})
	}
}

func TestBuildRawURL_GitLab(t *testing.T) {
	client := NewGitClient("")

	tests := []struct {
		name      string
		repoURL   string
		chartPath string
		branch    string
		expected  string
	}{
		{
			name:      "gitlab root chart",
			repoURL:   "https://gitlab.com/org/repo",
			chartPath: "",
			branch:    "main",
			expected:  "https://gitlab.com/org/repo/-/raw/main/Chart.yaml",
		},
		{
			name:      "gitlab with path",
			repoURL:   "https://gitlab.com/org/repo",
			chartPath: "charts/app",
			branch:    "main",
			expected:  "https://gitlab.com/org/repo/-/raw/main/charts/app/Chart.yaml",
		},
		{
			name:      "self-hosted gitlab",
			repoURL:   "https://gitlab.company.com/team/repo",
			chartPath: "",
			branch:    "production",
			expected:  "https://gitlab.company.com/team/repo/-/raw/production/Chart.yaml",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := client.buildRawURL(tt.repoURL, tt.chartPath, tt.branch)
			if got != tt.expected {
				t.Errorf("buildRawURL() = %s, want %s", got, tt.expected)
			}
		})
	}
}

func TestParseChartYaml_JSON(t *testing.T) {
	data := []byte(`{
		"name": "argo-cd",
		"version": "7.3.0",
		"dependencies": [
			{"name": "redis", "version": "7.2.4", "repository": "https://charts.bitnami.com/bitnami"},
			{"name": "redis-ha", "version": "4.22.0", "repository": "https://dandydeveloper.github.io/charts"}
		]
	}`)

	chart, err := parseChartYaml(data)
	if err != nil {
		t.Fatalf("parseChartYaml() error: %v", err)
	}

	if chart.Name != "argo-cd" {
		t.Errorf("expected name 'argo-cd', got '%s'", chart.Name)
	}
	if chart.Version != "7.3.0" {
		t.Errorf("expected version '7.3.0', got '%s'", chart.Version)
	}
	if len(chart.Dependencies) != 2 {
		t.Fatalf("expected 2 dependencies, got %d", len(chart.Dependencies))
	}
	if chart.Dependencies[0].Name != "redis" {
		t.Errorf("expected dep[0].name 'redis', got '%s'", chart.Dependencies[0].Name)
	}
	if chart.Dependencies[0].Version != "7.2.4" {
		t.Errorf("expected dep[0].version '7.2.4', got '%s'", chart.Dependencies[0].Version)
	}
	if chart.Dependencies[0].Repository != "https://charts.bitnami.com/bitnami" {
		t.Errorf("expected dep[0].repository 'https://charts.bitnami.com/bitnami', got '%s'", chart.Dependencies[0].Repository)
	}
}

func TestParseChartYaml_YAML(t *testing.T) {
	data := []byte(`name: cert-manager
version: 1.13.0
dependencies:
  - name: cainjector
    version: 1.13.0
    repository: https://charts.jetstack.io
  - name: webhook
    version: 1.13.0
    repository: https://charts.jetstack.io
`)

	chart, err := parseChartYaml(data)
	if err != nil {
		t.Fatalf("parseChartYaml() error: %v", err)
	}

	if chart.Name != "cert-manager" {
		t.Errorf("expected name 'cert-manager', got '%s'", chart.Name)
	}
	if chart.Version != "1.13.0" {
		t.Errorf("expected version '1.13.0', got '%s'", chart.Version)
	}
	if len(chart.Dependencies) != 2 {
		t.Fatalf("expected 2 dependencies, got %d", len(chart.Dependencies))
	}
	if chart.Dependencies[0].Name != "cainjector" {
		t.Errorf("expected dep[0].name 'cainjector', got '%s'", chart.Dependencies[0].Name)
	}
}

func TestParseChartYaml_NoDependencies(t *testing.T) {
	data := []byte(`name: simple-chart
version: 1.0.0
`)

	chart, err := parseChartYaml(data)
	if err != nil {
		t.Fatalf("parseChartYaml() error: %v", err)
	}

	if chart.Name != "simple-chart" {
		t.Errorf("expected name 'simple-chart', got '%s'", chart.Name)
	}
	if len(chart.Dependencies) != 0 {
		t.Errorf("expected 0 dependencies, got %d", len(chart.Dependencies))
	}
}

func TestFetchChartYaml_Success(t *testing.T) {
	// Mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check auth header
		auth := r.Header.Get("Authorization")
		if auth != "token test-token" {
			t.Errorf("expected auth 'token test-token', got '%s'", auth)
		}

		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte(`name: test-chart
version: 2.0.0
dependencies:
  - name: dep1
    version: 1.0.0
    repository: https://charts.example.com
`))
	}))
	defer server.Close()

	client := NewGitClient("test-token")
	ctx := context.Background()

	// Override the raw URL building by using the server URL directly
	chart, err := client.fetchRaw(ctx, server.URL)
	if err != nil {
		t.Fatalf("fetchRaw() error: %v", err)
	}

	if chart.Name != "test-chart" {
		t.Errorf("expected name 'test-chart', got '%s'", chart.Name)
	}
	if chart.Version != "2.0.0" {
		t.Errorf("expected version '2.0.0', got '%s'", chart.Version)
	}
	if len(chart.Dependencies) != 1 {
		t.Fatalf("expected 1 dependency, got %d", len(chart.Dependencies))
	}
}

func TestFetchChartYaml_404(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("Not Found"))
	}))
	defer server.Close()

	client := NewGitClient("")
	ctx := context.Background()

	_, err := client.fetchRaw(ctx, server.URL)
	if err == nil {
		t.Fatal("expected error for 404 response")
	}
	if !contains(err.Error(), "404") {
		t.Errorf("expected error to contain '404', got: %v", err)
	}
}

func TestFetchChartYaml_401(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte("Unauthorized"))
	}))
	defer server.Close()

	client := NewGitClient("bad-token")
	ctx := context.Background()

	_, err := client.fetchRaw(ctx, server.URL)
	if err == nil {
		t.Fatal("expected error for 401 response")
	}
	if !contains(err.Error(), "401") {
		t.Errorf("expected error to contain '401', got: %v", err)
	}
}

func TestFetchChartYaml_NoAuth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "" {
			t.Errorf("expected no auth header, got '%s'", auth)
		}

		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte(`name: public-chart
version: 1.0.0
`))
	}))
	defer server.Close()

	client := NewGitClient("") // No token
	ctx := context.Background()

	chart, err := client.fetchRaw(ctx, server.URL)
	if err != nil {
		t.Fatalf("fetchRaw() error: %v", err)
	}

	if chart.Name != "public-chart" {
		t.Errorf("expected name 'public-chart', got '%s'", chart.Name)
	}
}

func TestFetchChartYaml_ContextCancel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`name: slow-chart
version: 1.0.0
`))
	}))
	defer server.Close()

	client := NewGitClient("")
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, err := client.fetchRaw(ctx, server.URL)
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
