package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	artifactHubBaseURL = "https://artifacthub.io/api/v1"
)

// ArtifactHubClient is a client for interacting with the Artifact Hub API.
type ArtifactHubClient struct {
	BaseURL    string
	httpClient *http.Client
}

// NewArtifactHubClient creates a new Artifact Hub client.
func NewArtifactHubClient() *ArtifactHubClient {
	return &ArtifactHubClient{
		BaseURL: artifactHubBaseURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// HelmPackage represents the fields we need from the Artifact Hub API response for a Helm package.
type HelmPackage struct {
	AvailableVersions []struct {
		Version string `json:"version"`
	} `json:"available_versions"`
}

// GetChartVersions fetches the available versions for a Helm chart from Artifact Hub.
func (c *ArtifactHubClient) GetChartVersions(ctx context.Context, repoName, chartName string) ([]string, error) {
	url := fmt.Sprintf("%s/packages/helm/%s/%s", c.BaseURL, repoName, chartName)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body) // Read body for logging, ignore error
		return nil, fmt.Errorf("unexpected status code: %s. Body: %s", resp.Status, string(body))
	}

	var pkg HelmPackage
	if err := json.NewDecoder(resp.Body).Decode(&pkg); err != nil {
		// A more robust implementation would read to a buffer first to log the body on error.
		// For now, this is a known limitation.
		return nil, fmt.Errorf("failed to decode response body: %w", err)
	}

	var versionStrings []string
	for _, v := range pkg.AvailableVersions {
		versionStrings = append(versionStrings, v.Version)
	}

	return versionStrings, nil
}

// ArtifactHubSearchPackage represents a single package in the search results.
type ArtifactHubSearchPackage struct {
	PackageID      string `json:"package_id"`
	Name           string `json:"name"`
	NormalizedName string `json:"normalized_name"`
	Repository     struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	} `json:"repository"`
}

// ArtifactHubSearchResponse is the response from the search API.
type ArtifactHubSearchResponse struct {
	Packages []ArtifactHubSearchPackage `json:"packages"`
}

// SearchChart searches for a Helm chart on Artifact Hub and returns the repo and chart name of the best match.
func (c *ArtifactHubClient) SearchChart(ctx context.Context, chartName string) (repoName, officialChartName string, err error) {
	url := fmt.Sprintf("%s/packages/search?ts_query_web=%s&kind=0", c.BaseURL, chartName)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", "", fmt.Errorf("failed to create search request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("failed to execute search request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("search request failed with status: %s", resp.Status)
	}

	var searchResponse ArtifactHubSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&searchResponse); err != nil {
		return "", "", fmt.Errorf("failed to decode search response: %w", err)
	}

	if len(searchResponse.Packages) == 0 {
		return "", "", fmt.Errorf("no packages found for chart %s", chartName)
	}

	// Return the first result. This is a heuristic.
	bestMatch := searchResponse.Packages[0]
	return bestMatch.Repository.Name, bestMatch.Name, nil
}
