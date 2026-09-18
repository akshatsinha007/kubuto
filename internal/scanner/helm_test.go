package scanner

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/akshatsinha007/kubuto/internal/client"
	"github.com/akshatsinha007/kubuto/pkg/types"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/release"
)

// Helper function to create a mock release
func newMockRelease(chartName, chartVersion string, annotations map[string]string, sources []string) *release.Release {
	return &release.Release{
		Name: "test-release",
		Info: &release.Info{Status: release.StatusDeployed},
		Chart: &chart.Chart{
			Metadata: &chart.Metadata{
				Name:        chartName,
				Version:     chartVersion,
				Annotations: annotations,
				Sources:     sources,
			},
		},
	}
}

func TestProcessRelease(t *testing.T) {
	// Test case 1: Found via Annotation
	t.Run("Found via Annotation", func(t *testing.T) {
		// Setup mock Artifact Hub server
		mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/packages/helm/my-repo/my-chart" {
				fmt.Fprint(w, `{"available_versions": [{"version": "1.1.0"}]}`)
				return
			}
			http.NotFound(w, r)
		}))
		defer mockServer.Close()

		ahClient := client.NewArtifactHubClient()
		ahClient.BaseURL = mockServer.URL

		helmClient := client.NewHelmClient() // Not used in this test case, but needed for constructor
		scanner := NewHelmScanner(nil, ahClient, helmClient, logr.Discard())
		rel := newMockRelease("my-chart", "1.0.0", map[string]string{artifactHubRepoAnnotation: "my-repo"}, nil)

		resource := scanner.processRelease(context.Background(), rel, ScanOptions{})
		assert.Equal(t, "1.1.0", resource.LatestVersion)
		assert.Equal(t, types.UpdateStatusMinor, resource.UpdateStatus)
	})

	// Test case 2: Found via Search Fallback
	t.Run("Found via Search Fallback", func(t *testing.T) {
		// Setup mock Artifact Hub server
		mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/packages/search" {
				fmt.Fprintln(w, `{"packages": [{"name": "my-chart", "repository": {"name": "found-repo"}}]}`)
				return
			}
			if r.URL.Path == "/packages/helm/found-repo/my-chart" {
				fmt.Fprint(w, `{"available_versions": [{"version": "2.0.0"}]}`)
				return
			}
			http.NotFound(w, r)
		}))
		defer mockServer.Close()

		ahClient := client.NewArtifactHubClient()
		ahClient.BaseURL = mockServer.URL

		helmClient := client.NewHelmClient()
		scanner := NewHelmScanner(nil, ahClient, helmClient, logr.Discard())
		rel := newMockRelease("my-chart", "1.0.0", nil, nil) // No annotations or sources

		resource := scanner.processRelease(context.Background(), rel, ScanOptions{})
		assert.Equal(t, "2.0.0", resource.LatestVersion)
		assert.Equal(t, types.UpdateStatusMajor, resource.UpdateStatus)
	})

	// Test case 3: Found via sources field
	t.Run("Found via sources field", func(t *testing.T) {
		// Mock server for the Helm repository's index.yaml
		mockRepoServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/index.yaml" {
				fmt.Fprint(w, `
apiVersion: v1
entries:
  my-chart:
  - version: 3.0.0
  - version: 2.5.0
`)
				return
			}
			http.NotFound(w, r)
		}))
		defer mockRepoServer.Close()

		// The source URL will include the /index.yaml path
		sourceURL := mockRepoServer.URL + "/index.yaml"

		ahClient := client.NewArtifactHubClient()
		helmClient := client.NewHelmClient()
		helmClient.HTTPClient = mockRepoServer.Client() // Point helm client to mock server

		scanner := NewHelmScanner(nil, ahClient, helmClient, logr.Discard())
		rel := newMockRelease("my-chart", "1.0.0", nil, []string{sourceURL})

		resource := scanner.processRelease(context.Background(), rel, ScanOptions{})
		assert.Equal(t, "3.0.0", resource.LatestVersion)
		assert.Equal(t, types.UpdateStatusMajor, resource.UpdateStatus)
	})
}
