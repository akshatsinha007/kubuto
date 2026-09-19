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

package client

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// countingTransport counts real RoundTrip invocations so a test can
// assert exactly how many HTTP calls actually happened, not just that
// the right final answer came back (which caching bugs can still get
// right by accident on a single-threaded test).
type countingTransport struct {
	calls      int32
	respBody   string
	statusCode int
}

func (t *countingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	atomic.AddInt32(&t.calls, 1)
	status := t.statusCode
	if status == 0 {
		status = http.StatusOK
	}
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(bytes.NewBufferString(t.respBody)),
		Header:     make(http.Header),
	}, nil
}

const sampleIndex = `
apiVersion: v1
entries:
  my-chart:
    - name: my-chart
      version: 1.2.3
  other-chart:
    - name: other-chart
      version: 2.0.0
`

func TestHelmClient_IndexCache_RepeatedLookupsOnSameRepoFetchOnce(t *testing.T) {
	transport := &countingTransport{respBody: sampleIndex}
	c := NewHelmClient()
	c.HTTPClient = &http.Client{Transport: transport}

	for i := 0; i < 5; i++ {
		versions, err := c.GetChartVersions(context.Background(), "http://fake-repo.com", "my-chart")
		require.NoError(t, err)
		assert.Equal(t, []string{"1.2.3"}, versions)
	}
	// Also request a *different* chart from the same repo — must still
	// hit the cached index, not trigger a second fetch.
	versions, err := c.GetChartVersions(context.Background(), "http://fake-repo.com", "other-chart")
	require.NoError(t, err)
	assert.Equal(t, []string{"2.0.0"}, versions)

	assert.EqualValues(t, 1, atomic.LoadInt32(&transport.calls),
		"6 lookups against the same repo (2 different chart names) must produce exactly 1 real HTTP fetch")
}

func TestHelmClient_IndexCache_DifferentReposFetchIndependently(t *testing.T) {
	transport := &countingTransport{respBody: sampleIndex}
	c := NewHelmClient()
	c.HTTPClient = &http.Client{Transport: transport}

	_, err := c.GetChartVersions(context.Background(), "http://repo-a.com", "my-chart")
	require.NoError(t, err)
	_, err = c.GetChartVersions(context.Background(), "http://repo-b.com", "my-chart")
	require.NoError(t, err)

	assert.EqualValues(t, 2, atomic.LoadInt32(&transport.calls),
		"two distinct repo URLs must each be fetched once, not deduped against each other")
}

func TestHelmClient_IndexCache_ErrorIsAlsoCached(t *testing.T) {
	transport := &countingTransport{statusCode: http.StatusNotFound, respBody: "not found"}
	c := NewHelmClient()
	c.HTTPClient = &http.Client{Transport: transport}

	_, err1 := c.GetChartVersions(context.Background(), "http://broken-repo.com", "my-chart")
	require.Error(t, err1)
	_, err2 := c.GetChartVersions(context.Background(), "http://broken-repo.com", "my-chart")
	require.Error(t, err2)

	assert.EqualValues(t, 1, atomic.LoadInt32(&transport.calls),
		"a 404 must be cached too — retrying the same broken repo for every resource that shares it is exactly the redundant-fetch problem being fixed")
}

func TestHelmClient_IndexCache_ConcurrentLookupsCollapseIntoOneFetch(t *testing.T) {
	transport := &countingTransport{respBody: sampleIndex}
	c := NewHelmClient()
	c.HTTPClient = &http.Client{Transport: transport}

	const n = 50
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := c.GetChartVersions(context.Background(), "http://fake-repo.com", "my-chart")
			assert.NoError(t, err)
		}()
	}
	wg.Wait()

	assert.EqualValues(t, 1, atomic.LoadInt32(&transport.calls),
		"50 concurrent lookups against the same repo must collapse into 1 fetch via singleflight, not a thundering herd")
}

// zeroValueLiteral guards against the exact panic this fix caused
// before it was corrected: a HelmClient built as a struct literal
// (skipping NewHelmClient) must not panic on first use.
func TestHelmClient_ZeroValueLiteral_DoesNotPanic(t *testing.T) {
	transport := &countingTransport{respBody: sampleIndex}
	c := &HelmClient{HTTPClient: &http.Client{Transport: transport}}

	assert.NotPanics(t, func() {
		_, err := c.GetChartVersions(context.Background(), "http://fake-repo.com", "my-chart")
		require.NoError(t, err)
	})
}
