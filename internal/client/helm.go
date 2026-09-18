package client

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// HelmClient is a client for interacting with Helm repositories.
type HelmClient struct {
	HTTPClient *http.Client
}

// NewHelmClient creates a new Helm client.
func NewHelmClient() *HelmClient {
	return &HelmClient{
		HTTPClient: &http.Client{
			Timeout: 60 * time.Second, // Increased timeout for potentially large index files
		},
	}
}

// IndexFile represents the structure of a Helm repository index.yaml file.
type IndexFile struct {
	APIVersion string                    `yaml:"apiVersion"`
	Entries    map[string][]ChartVersion `yaml:"entries"`
}

// ChartVersion represents a single entry for a chart in the index.yaml file.
type ChartVersion struct {
	Name    string `yaml:"name"`
	Version string `yaml:"version"`
	// Other fields like 'created', 'digest', 'urls' are ignored for now.
}

// GetChartVersions fetches the available versions for a chart from a given repository URL.
func (c *HelmClient) GetChartVersions(ctx context.Context, repoURL, chartName string) ([]string, error) {
	index, err := c.downloadIndexFile(ctx, repoURL)
	if err != nil {
		return nil, fmt.Errorf("failed to download index file from %s: %w", repoURL, err)
	}

	chartEntries, ok := index.Entries[chartName]
	if !ok {
		return nil, fmt.Errorf("chart %q not found in repository %s", chartName, repoURL)
	}

	var versions []string
	for _, entry := range chartEntries {
		versions = append(versions, entry.Version)
	}

	return versions, nil
}

// downloadIndexFile fetches and parses the index.yaml from a repository URL.
func (c *HelmClient) downloadIndexFile(ctx context.Context, repoURL string) (*IndexFile, error) {
	// Ensure the URL ends with a slash so we can append 'index.yaml'
	if !strings.HasSuffix(repoURL, "/") {
		repoURL += "/"
	}
	indexURL := repoURL + "index.yaml"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, indexURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %s", resp.Status)
	}

	var indexFile IndexFile
	if err := yaml.NewDecoder(resp.Body).Decode(&indexFile); err != nil {
		return nil, fmt.Errorf("failed to decode index.yaml: %w", err)
	}

	return &indexFile, nil
}
