// Package engine is the CLI's HTTP client for the Kubuto Engine API.
//
// The wire contract is documented in `kubuto-engine/api/openapi.yaml`. This
// package owns the Go-side shape of every request and response and is the
// single source of truth for:
//
//   - The auth scheme: `Authorization: Bearer <api-key>`
//   - Rate-limit signalling: `X-RateLimit-*` headers (NOT `Retry-After`)
//   - Response field names that match the engine 1:1 (no synonyms, no drift)
//
// If you find yourself editing this file because the engine changed, please
// regenerate `internal/engine/client_test.go` as well — they are paired.
package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/akshatsinha007/kubuto/internal/cache"
)

// RateLimitError is returned by API calls that hit the engine's per-key
// rate limit. The CLI surfaces this directly to the user so they can either
// wait or upgrade their plan.
//
// `RetryAfter` is reconstructed from the engine's `X-RateLimit-Reset`
// header (a Unix epoch second). Older deployments may also emit
// `Retry-After`; we fall back to that for compatibility.
type RateLimitError struct {
	RetryAfter time.Duration
	Limit      int
	Remaining  int
	Message    string
}

func (e *RateLimitError) Error() string {
	if e.RetryAfter > 0 {
		return fmt.Sprintf("rate limit exceeded: %s (retry after %v)", e.Message, e.RetryAfter)
	}
	return fmt.Sprintf("rate limit exceeded: %s", e.Message)
}

// BatchTooSmallError is returned by the cluster compatibility endpoint
// when fewer than `MinBatchSize` charts were submitted. It mirrors
// `types.BatchTooSmallError` on the engine side.
//
// IMPORTANT: `ActionableCommand` is the engine's own message and MUST
// be surfaced to the user verbatim. The CLI does not reformat it — that
// keeps the upgrade-path advice consistent across CLI versions even
// when the engine evolves it (PRICING_ANALYSIS.md decision D1).
type BatchTooSmallError struct {
	MinBatchSize      int    `json:"min_batch_size"`
	ReceivedSize      int    `json:"received_size"`
	ActionableCommand string `json:"actionable_command"`
	Rationale         string `json:"rationale"`
}

func (e *BatchTooSmallError) Error() string {
	// Format engineered for `cmd.PrintErrln(err)` to read cleanly.
	if e.ActionableCommand != "" {
		return fmt.Sprintf("batch too small: %d/%d charts. %s Run: %s",
			e.ReceivedSize, e.MinBatchSize, e.Rationale, e.ActionableCommand)
	}
	return fmt.Sprintf("batch too small: received %d, minimum %d", e.ReceivedSize, e.MinBatchSize)
}

// Client is the HTTP client for communicating with the Kubuto Engine API.
type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
	cache      *cache.Cache
}

// New creates a new Engine client.
func New(baseURL, apiKey string, timeout time.Duration) (*Client, error) {
	if baseURL == "" {
		return nil, fmt.Errorf("baseURL is required")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("apiKey is required")
	}
	return &Client{
		baseURL: baseURL,
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: timeout,
		},
		cache: cache.New(1*time.Hour, 5*time.Minute),
	}, nil
}

// CompatResponse mirrors `types.CompatResponse` in `kubuto-engine`. Fields
// added on the server (e.g. `IsStale`, `ScrapedAt`) MUST also be added here
// or they will be silently dropped by `json.Decode`.
type CompatResponse struct {
	Chart      string `json:"chart"`
	Version    string `json:"version"`
	K8sVersion string `json:"k8s_version"`
	Compatible bool   `json:"compatible"`
	Status     string `json:"status"` // "found" | "no_data" | "not_tracked"
	Message    string `json:"message,omitempty"`
	Source     string `json:"source,omitempty"`
	ScrapedAt  string `json:"scraped_at,omitempty"`
	IsStale    bool   `json:"is_stale"`
}

// RecommendResponse mirrors `types.RecommendResponse` in `kubuto-engine`.
// Returned by GetRecommendedVersion to answer "what chart version should I
// install for K8s X?".
type RecommendResponse struct {
	Chart              string `json:"chart"`
	K8sVersion         string `json:"k8s_version"`
	RecommendedVersion string `json:"recommended_version,omitempty"`
	Status             string `json:"status"` // "found" | "no_data" | "not_tracked"
	Source             string `json:"source,omitempty"`
	ScrapedAt          string `json:"scraped_at,omitempty"`
	IsStale            bool   `json:"is_stale"`
	Message            string `json:"message,omitempty"`
}

// DiscoveredRequest is the body for POST /api/v1/discovered. Field names
// must match `types.DiscoveredChartRequest` in the engine repo exactly —
// gin's `binding:"required"` rejects requests with unknown shapes.
type DiscoveredRequest struct {
	ChartName      string `json:"chart_name"`
	Repository     string `json:"repository"`
	CurrentVersion string `json:"current_version"`
	LatestVersion  string `json:"latest_version,omitempty"`
	Status         string `json:"status,omitempty"`
}

// ValidateKeyResponse is returned by POST /api/v1/auth/validate.
type ValidateKeyResponse struct {
	Valid        bool   `json:"valid"`
	Plan         string `json:"plan,omitempty"`
	RateLimit    int    `json:"rate_limit,omitempty"`
	RequestsUsed int    `json:"requests_used,omitempty"`
}

// HealthCheck verifies the engine is reachable. The /health endpoint is
// public; we still send the auth header for parity with other calls so
// reverse-proxies that strip headers behave consistently.
func (c *Client) HealthCheck(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/health", nil)
	if err != nil {
		return err
	}
	c.setAuthHeader(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health check failed: %s", resp.Status)
	}
	return nil
}

// setAuthHeader attaches the Bearer token. The engine's auth middleware
// rejects anything else with 401.
func (c *Client) setAuthHeader(req *http.Request) {
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
}

// handleResponse maps non-success status codes to typed errors. Only 200
// and 202 propagate a nil error to the caller.
//
// Rate-limit detection prefers `X-RateLimit-Reset` (a Unix-second deadline)
// since that is what the engine emits today. We fall back to
// `Retry-After` (seconds offset) for older deployments.
func (c *Client) handleResponse(resp *http.Response) error {
	if resp.StatusCode == http.StatusTooManyRequests {
		retryAfter := 30 * time.Second // default
		if resetStr := resp.Header.Get("X-RateLimit-Reset"); resetStr != "" {
			if resetUnix, err := strconv.ParseInt(resetStr, 10, 64); err == nil {
				delta := time.Until(time.Unix(resetUnix, 0))
				if delta > 0 {
					retryAfter = delta
				}
			}
		} else if retry := resp.Header.Get("Retry-After"); retry != "" {
			if seconds, err := strconv.Atoi(retry); err == nil {
				retryAfter = time.Duration(seconds) * time.Second
			}
		}

		limit, _ := strconv.Atoi(resp.Header.Get("X-RateLimit-Limit"))
		remaining, _ := strconv.Atoi(resp.Header.Get("X-RateLimit-Remaining"))

		return &RateLimitError{
			RetryAfter: retryAfter,
			Limit:      limit,
			Remaining:  remaining,
			Message:    "Too many requests. Please slow down or upgrade your plan.",
		}
	}

	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("unauthorized: invalid or missing API key (run `kubuto auth validate` to test)")
	}

	// 422 from /compat/cluster carries a structured `batch_too_small` body
	// when the request had fewer than `MinBatchSize` charts. Decode it
	// into a typed error so callers can surface ActionableCommand verbatim.
	if resp.StatusCode == http.StatusUnprocessableEntity {
		body, _ := io.ReadAll(resp.Body)
		var probe struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &probe) == nil && probe.Error == "batch_too_small" {
			var bte BatchTooSmallError
			if err := json.Unmarshal(body, &bte); err == nil {
				return &bte
			}
		}
		return fmt.Errorf("unprocessable entity: %s", string(body))
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("unexpected status: %s", resp.Status)
	}

	return nil
}

// normalizeVersion strips the 'v' prefix from version strings.
func normalizeVersion(version string) string {
	if len(version) > 0 && version[0] == 'v' {
		return version[1:]
	}
	return version
}

// normalizeK8sVersion extracts major.minor from k8s version (e.g., "v1.28.0" -> "1.28").
func normalizeK8sVersion(k8s string) string {
	k8s = normalizeVersion(k8s)
	parts := strings.Split(k8s, ".")
	if len(parts) >= 2 {
		return parts[0] + "." + parts[1]
	}
	return k8s
}

// getCompat is the shared implementation behind GetCompat and
// CheckCompatNoCache — previously duplicated almost verbatim between them.
// When skipCache is true, neither the cache read nor the cache write
// happens, matching CheckCompatNoCache's existing contract: always hit the
// engine fresh (for `--no-cache`), and never populate the cache with a
// response the caller explicitly asked to bypass.
func (c *Client) getCompat(ctx context.Context, chartName, chartVersion, k8sVersion string, skipCache bool) (*CompatResponse, error) {
	chartVersion = normalizeVersion(chartVersion)
	k8sVersion = normalizeK8sVersion(k8sVersion)

	cacheKey := fmt.Sprintf("compat:%s:%s:%s", chartName, chartVersion, k8sVersion)
	if !skipCache {
		if cached, found := c.cache.Get(cacheKey); found {
			if resp, ok := cached.(*CompatResponse); ok {
				return resp, nil
			}
		}
	}

	url := fmt.Sprintf("%s/api/v1/charts/%s/compat?version=%s&k8s=%s", c.baseURL, chartName, chartVersion, k8sVersion)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	c.setAuthHeader(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if err := c.handleResponse(resp); err != nil {
		return nil, err
	}

	var compatResp CompatResponse
	if err := json.NewDecoder(resp.Body).Decode(&compatResp); err != nil {
		return nil, err
	}

	if !skipCache {
		c.cache.Set(cacheKey, &compatResp)
	}
	return &compatResp, nil
}

// GetCompat checks compatibility between a chart version and Kubernetes version.
func (c *Client) GetCompat(ctx context.Context, chartName, chartVersion, k8sVersion string) (*CompatResponse, error) {
	return c.getCompat(ctx, chartName, chartVersion, k8sVersion, false)
}

// CheckCompat is an alias for GetCompat kept for backwards compatibility
// with the scanner package. In practice this is the name every real call
// site uses (GetCompat is called directly only from tests) — kept as-is
// rather than reversing the deprecation direction without being asked.
func (c *Client) CheckCompat(ctx context.Context, chartName, chartVersion, k8sVersion string) (*CompatResponse, error) {
	return c.GetCompat(ctx, chartName, chartVersion, k8sVersion)
}

// CheckCompatNoCache forces a fresh fetch from the engine, skipping both
// the cache read and the cache write. Used when the user passes
// `--no-cache`.
func (c *Client) CheckCompatNoCache(ctx context.Context, chartName, chartVersion, k8sVersion string) (*CompatResponse, error) {
	return c.getCompat(ctx, chartName, chartVersion, k8sVersion, true)
}

// GetRecommendedVersion asks the engine for the highest-versioned chart
// that is compatible with `k8sVersion`. Use this to give the user an
// actionable upgrade hint when GetCompat returns `compatible=false`.
func (c *Client) GetRecommendedVersion(ctx context.Context, chartName, k8sVersion string) (*RecommendResponse, error) {
	k8sVersion = normalizeK8sVersion(k8sVersion)

	cacheKey := fmt.Sprintf("recommend:%s:%s", chartName, k8sVersion)
	if cached, found := c.cache.Get(cacheKey); found {
		if resp, ok := cached.(*RecommendResponse); ok {
			return resp, nil
		}
	}

	url := fmt.Sprintf("%s/api/v1/charts/%s/recommend?k8s=%s", c.baseURL, chartName, k8sVersion)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	c.setAuthHeader(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if err := c.handleResponse(resp); err != nil {
		return nil, err
	}

	var recResp RecommendResponse
	if err := json.NewDecoder(resp.Body).Decode(&recResp); err != nil {
		return nil, err
	}

	c.cache.Set(cacheKey, &recResp)
	return &recResp, nil
}

// LogDiscovered logs an untracked chart discovery to the engine. The
// /discovered endpoint is public (no auth required) but rate-limited.
func (c *Client) LogDiscovered(ctx context.Context, reqData DiscoveredRequest) error {
	body, err := json.Marshal(reqData)
	if err != nil {
		return fmt.Errorf("failed to marshal discovery request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v1/discovered", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Kubuto-CLI/v1.0")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if err := c.handleResponse(resp); err != nil {
		return err
	}
	return nil
}

// ValidateAPIKey calls POST /api/v1/auth/validate to check whether the
// configured API key is recognized by the engine and to surface the
// caller's plan + remaining quota. Used by `kubuto auth validate`.
func (c *Client) ValidateAPIKey(ctx context.Context) (*ValidateKeyResponse, error) {
	body := map[string]string{"api_key": c.apiKey}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal validate request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v1/auth/validate", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Kubuto-CLI/v1.0")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// 401 is meaningful for /auth/validate — surface it as an explicit
	// "invalid key" rather than the generic unauthorized message.
	if resp.StatusCode == http.StatusUnauthorized {
		return &ValidateKeyResponse{Valid: false}, nil
	}
	if err := c.handleResponse(resp); err != nil {
		return nil, err
	}

	var valResp ValidateKeyResponse
	if err := json.NewDecoder(resp.Body).Decode(&valResp); err != nil {
		return nil, err
	}
	return &valResp, nil
}
