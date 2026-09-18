// Copyright 2024 The Kubuto Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// The tests in this file lock the wire contract between the CLI engine
// client and the Kubuto Engine HTTP API. They use httptest servers so
// they run hermetically and stay green even when the engine is offline.
package engine

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNew exercises the constructor's input validation. baseURL and
// apiKey are both required; both should produce a typed error rather than
// a panic when missing.
func TestNew(t *testing.T) {
	tests := []struct {
		name      string
		baseURL   string
		apiKey    string
		timeout   time.Duration
		expectErr bool
	}{
		{
			name:      "valid client",
			baseURL:   "http://localhost:8080",
			apiKey:    "test-token",
			timeout:   30 * time.Second,
			expectErr: false,
		},
		{
			name:      "empty base URL",
			baseURL:   "",
			apiKey:    "test-token",
			timeout:   30 * time.Second,
			expectErr: true,
		},
		{
			name:      "empty apiKey",
			baseURL:   "http://localhost:8080",
			apiKey:    "",
			timeout:   30 * time.Second,
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := New(tt.baseURL, tt.apiKey, tt.timeout)
			if tt.expectErr {
				assert.Error(t, err)
				assert.Nil(t, client)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, client)
			}
		})
	}
}

// TestClient_GetCompat verifies:
//   - URL path and query-string normalization (`v` prefix stripped, K8s
//     truncated to major.minor).
//   - The `Authorization: Bearer ...` header is sent (NOT the legacy
//     `X-API-Key`).
//   - Response unmarshaling for each engine status (`found`, `not_tracked`).
//   - Rate-limit responses are surfaced as the typed RateLimitError.
func TestClient_GetCompat(t *testing.T) {
	tests := []struct {
		name            string
		responseBody    string
		statusCode      int
		expectCompat    bool
		expectStatus    string
		expectErr       bool
		expectRateLimit bool
	}{
		{
			name: "compatible chart",
			responseBody: `{
				"chart": "test-chart",
				"version": "1.2.3",
				"k8s_version": "1.28",
				"compatible": true,
				"status": "found",
				"source": "https://example.com",
				"is_stale": false
			}`,
			statusCode:   http.StatusOK,
			expectCompat: true,
			expectStatus: "found",
		},
		{
			name: "incompatible chart",
			responseBody: `{
				"chart": "test-chart",
				"version": "1.2.3",
				"k8s_version": "1.28",
				"compatible": false,
				"status": "found",
				"source": "https://example.com",
				"is_stale": true
			}`,
			statusCode:   http.StatusOK,
			expectCompat: false,
			expectStatus: "found",
		},
		{
			name: "not tracked chart",
			responseBody: `{
				"chart": "test-chart",
				"version": "1.2.3",
				"k8s_version": "1.28",
				"compatible": false,
				"status": "not_tracked",
				"message": "Chart not tracked"
			}`,
			statusCode:   http.StatusOK,
			expectCompat: false,
			expectStatus: "not_tracked",
		},
		{
			name:            "rate limit error",
			responseBody:    `{"error": "rate_limit"}`,
			statusCode:      http.StatusTooManyRequests,
			expectErr:       true,
			expectRateLimit: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "/api/v1/charts/test-chart/compat", r.URL.Path)
				assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"),
					"Should use Bearer auth header")

				query := r.URL.Query()
				assert.Equal(t, "1.2.3", query.Get("version"),
					"Chart version should have v prefix stripped")
				assert.Equal(t, "1.28", query.Get("k8s"),
					"K8s version should be normalized to major.minor")

				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.statusCode)
				_, _ = w.Write([]byte(tt.responseBody))
			}))
			defer server.Close()

			client, err := New(server.URL, "test-token", 30*time.Second)
			require.NoError(t, err)

			// Use a unique chart name per subtest so the in-memory cache
			// does not leak between cases.
			resp, err := client.GetCompat(context.Background(), "test-chart", "v1.2.3", "v1.28.0")

			if tt.expectErr {
				assert.Error(t, err)
				if tt.expectRateLimit {
					assert.IsType(t, &RateLimitError{}, err)
				}
			} else {
				require.NoError(t, err)
				assert.NotNil(t, resp)
				assert.Equal(t, tt.expectCompat, resp.Compatible)
				assert.Equal(t, tt.expectStatus, resp.Status)
			}
		})
	}
}

// TestClient_CheckCompatNoCache asserts that the no-cache code path actually
// hits the network on every call rather than reading from the in-memory
// cache. We count requests on the test server.
func TestClient_CheckCompatNoCache(t *testing.T) {
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"chart":"chart","version":"1.0.0","k8s_version":"1.28","compatible":true,"status":"found"}`))
	}))
	defer server.Close()

	client, err := New(server.URL, "test-token", 30*time.Second)
	require.NoError(t, err)

	resp1, err := client.CheckCompatNoCache(context.Background(), "chart", "v1.0.0", "1.28")
	require.NoError(t, err)
	assert.True(t, resp1.Compatible)

	resp2, err := client.CheckCompatNoCache(context.Background(), "chart", "v1.0.0", "1.28")
	require.NoError(t, err)
	assert.True(t, resp2.Compatible)

	assert.Equal(t, 2, hits, "no-cache mode must hit the server every time")
}

// TestClient_GetRecommendedVersion exercises the new /recommend endpoint.
// This is the upgrade-hint pathway used by the CLI when GetCompat returns
// "compatible=false" or "no_data".
func TestClient_GetRecommendedVersion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/charts/test-chart/recommend", r.URL.Path)
		assert.Equal(t, "1.28", r.URL.Query().Get("k8s"))
		assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"chart": "test-chart",
			"k8s_version": "1.28",
			"recommended_version": "2.5.0",
			"status": "found",
			"source": "https://example.com",
			"scraped_at": "2025-01-15T00:00:00Z",
			"is_stale": false
		}`))
	}))
	defer server.Close()

	client, err := New(server.URL, "test-token", 30*time.Second)
	require.NoError(t, err)

	resp, err := client.GetRecommendedVersion(context.Background(), "test-chart", "v1.28.0")
	require.NoError(t, err)
	assert.Equal(t, "found", resp.Status)
	assert.Equal(t, "2.5.0", resp.RecommendedVersion)
	assert.Equal(t, "1.28", resp.K8sVersion)
}

// TestClient_LogDiscovered locks the /discovered payload schema. The
// engine binds against `types.DiscoveredChartRequest` with required
// fields, so any drift in field names will fail this test loudly.
func TestClient_LogDiscovered(t *testing.T) {
	t.Run("successful discovery logging", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/api/v1/discovered", r.URL.Path)
			assert.Equal(t, "Kubuto-CLI/v1.0", r.Header.Get("User-Agent"))
			assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
			// /discovered is public — no auth header expected.
			assert.Empty(t, r.Header.Get("Authorization"),
				"Discovered endpoint should not send auth")

			w.WriteHeader(http.StatusAccepted)
		}))
		defer server.Close()

		client, err := New(server.URL, "test-token", 30*time.Second)
		require.NoError(t, err)

		err = client.LogDiscovered(context.Background(), DiscoveredRequest{
			ChartName:      "prometheus",
			Repository:     "https://prometheus-community.github.io/helm-charts",
			CurrentVersion: "15.0.0",
			Status:         "discovered",
		})
		assert.NoError(t, err)
	})

	t.Run("rate limit on discovery", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			// Exercise the legacy Retry-After fallback path.
			w.Header().Set("Retry-After", "60")
			w.WriteHeader(http.StatusTooManyRequests)
		}))
		defer server.Close()

		client, err := New(server.URL, "test-token", 30*time.Second)
		require.NoError(t, err)

		err = client.LogDiscovered(context.Background(), DiscoveredRequest{
			ChartName:      "test",
			CurrentVersion: "1.0.0",
		})
		assert.Error(t, err)
		assert.IsType(t, &RateLimitError{}, err)
		rateErr := err.(*RateLimitError)
		assert.Equal(t, 60*time.Second, rateErr.RetryAfter)
	})
}

// TestClient_ValidateAPIKey covers both the "valid key" success path and
// the "invalid key" path that returns a 401. The CLI must not raise this
// 401 as an error — it is a meaningful, expected verdict.
func TestClient_ValidateAPIKey(t *testing.T) {
	t.Run("valid key", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/api/v1/auth/validate", r.URL.Path)
			assert.Equal(t, http.MethodPost, r.Method)

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"valid": true,
				"plan": "pro",
				"rate_limit": 1000,
				"requests_used": 42
			}`))
		}))
		defer server.Close()

		client, err := New(server.URL, "test-token", 30*time.Second)
		require.NoError(t, err)

		resp, err := client.ValidateAPIKey(context.Background())
		require.NoError(t, err)
		assert.True(t, resp.Valid)
		assert.Equal(t, "pro", resp.Plan)
		assert.Equal(t, 1000, resp.RateLimit)
		assert.Equal(t, 42, resp.RequestsUsed)
	})

	t.Run("invalid key returns valid=false (not error)", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}))
		defer server.Close()

		client, err := New(server.URL, "bad-token", 30*time.Second)
		require.NoError(t, err)

		resp, err := client.ValidateAPIKey(context.Background())
		require.NoError(t, err, "invalid key is a verdict, not an error")
		assert.False(t, resp.Valid)
	})
}

// TestClient_HealthCheck verifies the simple liveness endpoint works.
func TestClient_HealthCheck(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/health", r.URL.Path)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client, err := New(server.URL, "test-token", 30*time.Second)
	require.NoError(t, err)

	err = client.HealthCheck(context.Background())
	assert.NoError(t, err)
}

// TestClient_HandleResponse covers the status-code-to-error mapping in
// handleResponse, including the new X-RateLimit-* header preference.
func TestClient_HandleResponse(t *testing.T) {
	t.Run("OK response", func(t *testing.T) {
		client, _ := New("http://test", "token", 30*time.Second)
		resp := &http.Response{StatusCode: http.StatusOK}
		err := client.handleResponse(resp)
		assert.NoError(t, err)
	})

	t.Run("Rate limit prefers X-RateLimit-Reset", func(t *testing.T) {
		client, _ := New("http://test", "token", 30*time.Second)
		// Reset 90 seconds in the future. We allow a small slack since
		// time.Until() is computed live.
		future := strconv.FormatInt(time.Now().Add(90*time.Second).Unix(), 10)
		resp := &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Header: http.Header{
				"X-Ratelimit-Reset":     []string{future},
				"X-Ratelimit-Limit":     []string{"100"},
				"X-Ratelimit-Remaining": []string{"0"},
			},
		}
		err := client.handleResponse(resp)
		require.Error(t, err)
		rateErr, ok := err.(*RateLimitError)
		require.True(t, ok)
		assert.Equal(t, 100, rateErr.Limit)
		assert.Equal(t, 0, rateErr.Remaining)
		// Allow ±5s drift.
		delta := rateErr.RetryAfter
		assert.True(t, delta > 80*time.Second && delta < 95*time.Second,
			fmt.Sprintf("expected ~90s retry, got %v", delta))
	})

	t.Run("Rate limit falls back to Retry-After", func(t *testing.T) {
		client, _ := New("http://test", "token", 30*time.Second)
		resp := &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Header:     http.Header{"Retry-After": []string{"120"}},
		}
		err := client.handleResponse(resp)
		require.Error(t, err)
		rateErr := err.(*RateLimitError)
		assert.Equal(t, 120*time.Second, rateErr.RetryAfter)
	})

	t.Run("Rate limit without any header uses default", func(t *testing.T) {
		client, _ := New("http://test", "token", 30*time.Second)
		resp := &http.Response{StatusCode: http.StatusTooManyRequests, Header: http.Header{}}
		err := client.handleResponse(resp)
		require.Error(t, err)
		rateErr := err.(*RateLimitError)
		assert.Equal(t, 30*time.Second, rateErr.RetryAfter, "Default retry should be 30s")
	})

	t.Run("Unauthorized produces a clear message", func(t *testing.T) {
		client, _ := New("http://test", "token", 30*time.Second)
		resp := &http.Response{StatusCode: http.StatusUnauthorized}
		err := client.handleResponse(resp)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unauthorized")
	})

	t.Run("Server error", func(t *testing.T) {
		client, _ := New("http://test", "token", 30*time.Second)
		resp := &http.Response{StatusCode: http.StatusInternalServerError, Status: "500 Internal Server Error"}
		err := client.handleResponse(resp)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "500 Internal Server Error")
	})

	// 422 with the engine's structured `batch_too_small` body must be
	// decoded into a typed *BatchTooSmallError so callers (e.g. the
	// `compat cluster` command) can surface ActionableCommand verbatim.
	// This is the CLI half of the D1 price-leak fix — the engine half
	// lives in kubuto-engine/internal/api/compat_batch_test.go.
	t.Run("422 batch_too_small produces typed error", func(t *testing.T) {
		client, _ := New("http://test", "token", 30*time.Second)
		body := `{
			"error":"batch_too_small",
			"min_batch_size":3,
			"received_size":1,
			"actionable_command":"kubuto compat chart <name>",
			"rationale":"Single-chart checks should use the per-chart endpoint."
		}`
		resp := &http.Response{
			StatusCode: http.StatusUnprocessableEntity,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     http.Header{"Content-Type": []string{"application/json"}},
		}
		err := client.handleResponse(resp)
		require.Error(t, err)

		bte, ok := err.(*BatchTooSmallError)
		require.True(t, ok, "expected *BatchTooSmallError, got %T (%v)", err, err)
		assert.Equal(t, 3, bte.MinBatchSize)
		assert.Equal(t, 1, bte.ReceivedSize)
		assert.Equal(t, "kubuto compat chart <name>", bte.ActionableCommand)
		assert.NotEmpty(t, bte.Rationale)

		// The Error() string should embed the actionable command so a
		// caller that just prints `err` still gives the user the next
		// step. Specifically protects against a future regression that
		// silently drops ActionableCommand from the message.
		assert.Contains(t, bte.Error(), "kubuto compat chart")
	})

	// Other 422s — different `error` value, malformed body, or unrelated
	// payloads — must NOT be miscast as BatchTooSmallError. They should
	// fall through to the generic "unprocessable entity" path so callers
	// surface the raw server message.
	t.Run("422 with non-matching body falls through to generic error", func(t *testing.T) {
		client, _ := New("http://test", "token", 30*time.Second)
		body := `{"error":"some_other_validation","message":"foo"}`
		resp := &http.Response{
			StatusCode: http.StatusUnprocessableEntity,
			Body:       io.NopCloser(strings.NewReader(body)),
		}
		err := client.handleResponse(resp)
		require.Error(t, err)

		_, ok := err.(*BatchTooSmallError)
		assert.False(t, ok, "non-batch_too_small 422 must not be typed as BatchTooSmallError")
		assert.Contains(t, err.Error(), "unprocessable entity")
	})

	t.Run("422 with malformed JSON body falls through gracefully", func(t *testing.T) {
		client, _ := New("http://test", "token", 30*time.Second)
		resp := &http.Response{
			StatusCode: http.StatusUnprocessableEntity,
			Body:       io.NopCloser(strings.NewReader("not even json")),
		}
		err := client.handleResponse(resp)
		require.Error(t, err)

		_, ok := err.(*BatchTooSmallError)
		assert.False(t, ok, "garbage 422 body must not be typed as BatchTooSmallError")
	})
}

func TestNormalizeVersion(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"v1.2.3", "1.2.3"},
		{"1.2.3", "1.2.3"},
		{"v1.28.0-gke.1", "1.28.0-gke.1"},
		{"", ""},
		{"v", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := normalizeVersion(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestNormalizeK8sVersion(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"v1.28.0", "1.28"},
		{"1.28.0", "1.28"},
		{"v1.28.0-gke.1", "1.28"},
		{"1.28", "1.28"},
		{"v1.2", "1.2"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := normalizeK8sVersion(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestRateLimitError(t *testing.T) {
	err := &RateLimitError{
		RetryAfter: 60 * time.Second,
		Message:    "Too many requests",
	}
	assert.Contains(t, err.Error(), "rate limit exceeded")
	assert.Contains(t, err.Error(), "Too many requests")
}
