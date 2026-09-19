package client

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
	"gopkg.in/yaml.v3"
)

// HelmClient is a client for interacting with Helm repositories.
//
// One HelmClient instance is shared across all three scanners (helm,
// argocd, flux) for the lifetime of a single `kubuto scan` — see
// RunClusterScan. A real cluster commonly has many releases/resources
// pointing at the same handful of chart repos (ingress-nginx,
// cert-manager, bitnami, ...), so `indexCache` memoizes each repo's
// index.yaml for that lifetime rather than re-fetching it once per
// resource. `indexFlight` additionally collapses concurrent requests
// for the same repo (now that scanners fetch in parallel, see
// parallelMap) into a single in-flight HTTP call instead of a
// thundering herd.
type HelmClient struct {
	HTTPClient *http.Client

	indexMu     sync.Mutex
	indexCache  map[string]indexCacheEntry
	indexFlight singleflight.Group
}

// indexCacheEntry caches both outcomes. A repo that 404s once will
// 404 identically for the rest of this process's lifetime (the client
// is never reused across separate `kubuto` invocations), so caching
// the error too avoids every resource sharing a broken repo URL
// repeating the same failed request.
type indexCacheEntry struct {
	index *IndexFile
	err   error
}

// NewHelmClient creates a new Helm client.
func NewHelmClient() *HelmClient {
	return &HelmClient{
		HTTPClient: &http.Client{
			Timeout: 60 * time.Second, // Increased timeout for potentially large index files
		},
		indexCache: make(map[string]indexCacheEntry),
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

// downloadIndexFile returns the parsed index.yaml for repoURL, fetching
// it at most once per HelmClient lifetime (see the cache/singleflight
// doc on HelmClient) regardless of how many resources share that repo
// or how many of those lookups happen concurrently.
func (c *HelmClient) downloadIndexFile(ctx context.Context, repoURL string) (*IndexFile, error) {
	c.indexMu.Lock()
	if c.indexCache == nil {
		// Lazily initialized rather than only in NewHelmClient: tests
		// and other callers legitimately build &HelmClient{...} literals
		// directly (e.g. to inject a mock HTTPClient), which must not
		// panic on a nil map just because they skipped the constructor.
		c.indexCache = make(map[string]indexCacheEntry)
	}
	if entry, ok := c.indexCache[repoURL]; ok {
		c.indexMu.Unlock()
		return entry.index, entry.err
	}
	c.indexMu.Unlock()

	v, err, _ := c.indexFlight.Do(repoURL, func() (interface{}, error) {
		index, ferr := c.fetchIndexFile(ctx, repoURL)

		c.indexMu.Lock()
		c.indexCache[repoURL] = indexCacheEntry{index: index, err: ferr}
		c.indexMu.Unlock()

		return index, ferr
	})
	if err != nil {
		return nil, err
	}
	return v.(*IndexFile), nil
}

// fetchIndexFile does the actual HTTP GET + YAML decode of a
// repository's index.yaml. Never call this directly — go through
// downloadIndexFile so concurrent/repeated lookups for the same repo
// share one result.
func (c *HelmClient) fetchIndexFile(ctx context.Context, repoURL string) (*IndexFile, error) {
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
