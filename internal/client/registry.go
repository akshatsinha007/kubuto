package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/akshatsinha007/kubuto/internal/config"
)

// RegistryClient defines the interface for a container registry client.
type RegistryClient interface {
	GetTags(ctx context.Context, image string) ([]string, error)
}

// defaultRegistryClient is the default implementation of RegistryClient.
type defaultRegistryClient struct {
	HTTPClient  *http.Client
	GitHubToken string
}

// NewRegistryClient creates a new default registry client.
func NewRegistryClient(cfg *config.Config) RegistryClient {
	token := ""
	if cfg.GitHub.Token != "" {
		token = cfg.GitHub.Token
	}
	return &defaultRegistryClient{
		HTTPClient:  &http.Client{Timeout: 30 * time.Second},
		GitHubToken: token,
	}
}

// GetTags fetches the available tags for a given image, dispatching to the correct registry handler.
func (c *defaultRegistryClient) GetTags(ctx context.Context, image string) ([]string, error) {
	imageName, registry := parseImageName(image)

	switch registry {
	case "docker.io":
		return c.getTagsFromDockerV2Registry(ctx, imageName, "mirror.gcr.io", "GCR Mirror")
	case "public.ecr.aws":
		return c.getTagsFromDockerV2Registry(ctx, imageName, "public.ecr.aws", "Public ECR")
	case "quay.io":
		return c.getTagsFromQuay(ctx, imageName)
	case "ghcr.io":
		return c.getTagsFromGhcr(ctx, imageName)
	default:
		return nil, fmt.Errorf("registry '%s' is not supported yet", registry)
	}
}

// --- Docker V2 Registry Compatible ---

type dockerV2TagsResponse struct {
	Tags []string `json:"tags"`
}

func (c *defaultRegistryClient) getTagsFromDockerV2Registry(ctx context.Context, imageName, registryHost, registryName string) ([]string, error) {
	var allTags []string
	url := fmt.Sprintf("https://%s/v2/%s/tags/list", registryHost, imageName)
	pagesFetched := 0
	maxPages := 5 // Limit to 5 pages to avoid timeouts on huge repositories

	for url != "" && pagesFetched < maxPages {
		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}

		resp, err := c.HTTPClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch tags from %s: %w", url, err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("failed to fetch tags from %s, status code: %d", registryName, resp.StatusCode)
		}

		var tagsResponse dockerV2TagsResponse
		if err := json.NewDecoder(resp.Body).Decode(&tagsResponse); err != nil {
			return nil, fmt.Errorf("failed to decode %s response: %w", registryName, err)
		}

		allTags = append(allTags, tagsResponse.Tags...)

		linkHeader := resp.Header.Get("Link")
		if linkHeader != "" {
			parts := strings.Split(linkHeader, ";")
			if len(parts) == 2 && strings.TrimSpace(parts[1]) == `rel="next"` {
				nextRelativeURL := strings.Trim(parts[0], "<> ")
				url = fmt.Sprintf("https://%s%s", registryHost, nextRelativeURL)
			} else {
				url = ""
			}
		} else {
			url = ""
		}
		pagesFetched++
	}

	return allTags, nil
}

// --- Quay.io ---

type quayTagsResponse struct {
	Tags []struct {
		Name string `json:"name"`
	} `json:"tags"`
	HasAdditional bool `json:"has_additional"`
}

func (c *defaultRegistryClient) getTagsFromQuay(ctx context.Context, imageName string) ([]string, error) {
	var allTags []string
	page := 1
	maxPages := 5 // Limit to 5 pages to avoid timeouts on huge repositories

	for {
		url := fmt.Sprintf("https://quay.io/api/v1/repository/%s/tag/?page=%d", imageName, page)
		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}

		resp, err := c.HTTPClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch tags from %s: %w", url, err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("failed to fetch tags from quay.io, status code: %d", resp.StatusCode)
		}

		var tagsResponse quayTagsResponse
		if err := json.NewDecoder(resp.Body).Decode(&tagsResponse); err != nil {
			return nil, fmt.Errorf("failed to decode quay.io response: %w", err)
		}

		for _, tag := range tagsResponse.Tags {
			allTags = append(allTags, tag.Name)
		}

		if !tagsResponse.HasAdditional || page >= maxPages {
			break
		}
		page++
	}

	return allTags, nil
}

// --- GitHub Container Registry (ghcr.io) ---

type ghcrVersionResponse struct {
	Name string `json:"name"` // This is the tag
}

func (c *defaultRegistryClient) getTagsFromGhcr(ctx context.Context, imageName string) ([]string, error) {
	parts := strings.Split(imageName, "/")
	if len(parts) < 2 {
		return nil, fmt.Errorf("invalid ghcr.io image format: %s", imageName)
	}
	owner := parts[0]
	pkgName := strings.Join(parts[1:], "/")

	// Try orgs endpoint first
	tags, err := c.fetchGhcrTags(ctx, fmt.Sprintf("https://api.github.com/orgs/%s/packages/container/%s/versions", owner, pkgName))
	if err != nil {
		// If orgs fails, try users endpoint
		tags, err = c.fetchGhcrTags(ctx, fmt.Sprintf("https://api.github.com/users/%s/packages/container/%s/versions", owner, pkgName))
		if err != nil {
			return nil, err // Return the error from the users endpoint attempt
		}
	}
	return tags, nil
}

func (c *defaultRegistryClient) fetchGhcrTags(ctx context.Context, url string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	if c.GitHubToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.GitHubToken)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to fetch tags from GitHub API, status: %s", resp.Status)
	}

	var versions []ghcrVersionResponse
	if err := json.NewDecoder(resp.Body).Decode(&versions); err != nil {
		return nil, fmt.Errorf("failed to decode ghcr response: %w", err)
	}

	var tags []string
	for _, v := range versions {
		tags = append(tags, v.Name)
	}
	return tags, nil
}

// parseImageName splits an image name into the repository name and the registry.
func parseImageName(image string) (name, registry string) {
	parts := strings.Split(image, "/")
	if len(parts) == 1 {
		return "library/" + image, "docker.io"
	}

	if strings.Contains(parts[0], ".") || strings.Contains(parts[0], ":") {
		registry = parts[0]
		name = strings.Join(parts[1:], "/")
		return name, registry
	}

	return image, "docker.io"
}
