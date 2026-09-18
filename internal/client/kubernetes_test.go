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
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/akshatsinha007/kubuto/internal/config"
)

func TestExtractMajorMinor(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"1.28.0", "1.28"},
		{"1.28.0-gke.1", "1.28"},
		{"1.27.3-eks-123456", "1.27"},
		{"1.28", "1.28"},
		{"1.2", "1.2"},
		{"1", "1"},
		{"", ""},
		{"v1.28.0", "v1.28"}, // Note: this function doesn't strip 'v'
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := extractMajorMinor(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestCurrentNamespace locks in the fix for a previously hardcoded
// "default" namespace: it must read the real current-context's namespace
// from kubeconfig, not just always return "default". Uses a real
// kubeconfig file (clientcmd's .Namespace() only parses YAML — it never
// contacts a cluster — so this needs no live cluster access).
func TestCurrentNamespace(t *testing.T) {
	kubeconfigYAML := `
apiVersion: v1
kind: Config
clusters:
- name: test-cluster
  cluster:
    server: https://example.invalid:6443
users:
- name: test-user
  user: {}
contexts:
- name: ctx-with-namespace
  context:
    cluster: test-cluster
    user: test-user
    namespace: my-namespace
- name: ctx-without-namespace
  context:
    cluster: test-cluster
    user: test-user
current-context: ctx-with-namespace
`
	dir := t.TempDir()
	path := filepath.Join(dir, "config")
	if err := os.WriteFile(path, []byte(kubeconfigYAML), 0o600); err != nil {
		t.Fatalf("write test kubeconfig: %v", err)
	}

	t.Run("reads the current context's namespace", func(t *testing.T) {
		ns, err := CurrentNamespace(&config.ClusterConfig{Kubeconfig: path})
		assert.NoError(t, err)
		assert.Equal(t, "my-namespace", ns)
	})

	t.Run("falls back to default when the context has no namespace set", func(t *testing.T) {
		ns, err := CurrentNamespace(&config.ClusterConfig{
			Kubeconfig: path,
			Context:    "ctx-without-namespace",
		})
		assert.NoError(t, err)
		assert.Equal(t, "default", ns)
	})
}

func TestGetClusterK8sVersion(t *testing.T) {
	t.Run("nil client returns error", func(t *testing.T) {
		version, err := GetClusterK8sVersion(nil)
		assert.Error(t, err)
		assert.Empty(t, version)
		assert.Contains(t, err.Error(), "kubernetes client is nil")
	})

	// Note: Testing with a real client would require a running cluster or mock
	// The actual version extraction is tested in TestExtractMajorMinor
}
