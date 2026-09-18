package client

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockTransport is a mock implementation of http.RoundTripper for testing.
type mockTransport struct {
	responseGenerator func() *http.Response
	err               error
}

// RoundTrip implements the http.RoundTripper interface.
func (t *mockTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.err != nil {
		return nil, t.err
	}
	return t.responseGenerator(), nil
}

func TestHelmClient_GetChartVersions(t *testing.T) {
	sampleIndexYAML := `
apiVersion: v1
entries:
  my-chart:
    - name: my-chart
      version: 1.2.3
    - name: my-chart
      version: 1.2.2
  another-chart:
    - name: another-chart
      version: 3.0.0
`

	// Create a mock HTTP client
	mockedClient := &http.Client{
		Transport: &mockTransport{
			responseGenerator: func() *http.Response {
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(bytes.NewBufferString(sampleIndexYAML)),
					Header:     make(http.Header),
				}
			},
		},
	}

	// Create the HelmClient with the mocked http.Client
	helmClient := &HelmClient{
		HTTPClient: mockedClient,
	}

	t.Run("Chart Found", func(t *testing.T) {
		versions, err := helmClient.GetChartVersions(context.Background(), "http://fake-repo.com", "my-chart")
		require.NoError(t, err)
		assert.Len(t, versions, 2, "Should find 2 versions for my-chart")
		assert.Contains(t, versions, "1.2.3")
		assert.Contains(t, versions, "1.2.2")
	})

	t.Run("Chart Not Found", func(t *testing.T) {
		_, err := helmClient.GetChartVersions(context.Background(), "http://fake-repo.com", "non-existent-chart")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "chart \"non-existent-chart\" not found", "Should return a not found error")
	})
}

func TestHelmClient_GetChartVersions_HttpError(t *testing.T) {
	// Create a mock HTTP client that returns an error status
	mockedClient := &http.Client{
		Transport: &mockTransport{
			responseGenerator: func() *http.Response {
				return &http.Response{
					StatusCode: http.StatusNotFound,
					Status:     "404 Not Found",
					Body:       io.NopCloser(bytes.NewBufferString("Not Found")),
					Header:     make(http.Header),
				}
			},
		},
	}

	helmClient := &HelmClient{
		HTTPClient: mockedClient,
	}

	_, err := helmClient.GetChartVersions(context.Background(), "http://fake-repo.com", "my-chart")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected status code: 404 Not Found")
}
