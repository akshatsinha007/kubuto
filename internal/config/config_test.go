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

package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetDefaultConfig(t *testing.T) {
	cfg := GetDefaultConfig()

	assert.NotNil(t, cfg)
	assert.Equal(t, 30*time.Second, cfg.Cluster.Timeout)
	assert.Equal(t, "http://localhost:8080", cfg.Engine.URL)
	assert.Equal(t, 30*time.Second, cfg.Engine.Timeout)
	assert.Equal(t, 10, cfg.Scanning.Concurrency)
	assert.Equal(t, 60*time.Second, cfg.Scanning.Timeout)

	// Test ArgoCD config defaults
	assert.False(t, cfg.Scanning.ArgoCD.Enabled, "ArgoCD should be disabled by default")
	assert.Equal(t, "argocd", cfg.Scanning.ArgoCD.Namespace)
	assert.Equal(t, "api", cfg.Scanning.ArgoCD.FetchMethod)
	assert.Empty(t, cfg.Scanning.ArgoCD.GitOps) // GitOps is nil/empty by default

	// Test Flux config defaults
	assert.False(t, cfg.Scanning.Flux.Enabled, "Flux should be disabled by default")
	assert.Equal(t, "flux-system", cfg.Scanning.Flux.SystemNamespace)

	// Test cache defaults
	assert.True(t, cfg.Cache.Enabled)
	assert.Equal(t, "~/.kubuto/cache", cfg.Cache.Directory)
	assert.Equal(t, 24*time.Hour, cfg.Cache.TTL)

	// Test output defaults
	assert.Equal(t, "table", cfg.Output.Format)
	assert.True(t, cfg.Output.ShowProgress)
	assert.Equal(t, "auto", cfg.Output.Colors)

	// Test logging defaults
	assert.Equal(t, "info", cfg.Logging.Level)
	assert.Equal(t, "text", cfg.Logging.Format)
}

func TestLoadGlobal(t *testing.T) {
	// Test that LoadGlobal returns a valid config (falls back to defaults if no file)
	cfg := LoadGlobal()
	assert.NotNil(t, cfg)
	assert.Equal(t, "http://localhost:8080", cfg.Engine.URL)
}

func TestLoadConfig(t *testing.T) {
	t.Run("non-existent file returns defaults", func(t *testing.T) {
		// When a specific path is given but doesn't exist, viper returns an error
		// This is expected behavior - user provided an invalid path
		cfg, err := LoadConfig("/non/existent/path/config.yaml")
		assert.Error(t, err)
		assert.Nil(t, cfg)
	})

	t.Run("empty path returns defaults when no config exists", func(t *testing.T) {
		// When no path is given, LoadConfig searches default locations
		// and returns defaults if no config file is found
		cfg, err := LoadConfig("")
		assert.NoError(t, err)
		assert.NotNil(t, cfg)
		assert.Equal(t, "http://localhost:8080", cfg.Engine.URL)
	})

	t.Run("valid config file", func(t *testing.T) {
		// Create a temporary config file
		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "config.yaml")

		configContent := `
engine:
  url: "https://custom-engine.example.com"
  timeout: 60s
  api_key: "test-api-key"

scanning:
  concurrency: 20
  timeout: 120s
  argocd:
    enabled: true
    namespace: "custom-argocd"
    fetch_method: "clone"
  flux:
    enabled: true
    system_namespace: "custom-flux"

cache:
  enabled: false
  ttl: 48h

output:
  format: "json"
  show_progress: false

logging:
  level: "debug"
  format: "json"
`
		err := os.WriteFile(configPath, []byte(configContent), 0644)
		require.NoError(t, err)

		cfg, err := LoadConfig(configPath)
		require.NoError(t, err)
		assert.NotNil(t, cfg)

		// Test engine config
		assert.Equal(t, "https://custom-engine.example.com", cfg.Engine.URL)
		assert.Equal(t, 60*time.Second, cfg.Engine.Timeout)
		assert.Equal(t, "test-api-key", cfg.Engine.APIKey)

		// Test scanning config
		assert.Equal(t, 20, cfg.Scanning.Concurrency)
		assert.Equal(t, 120*time.Second, cfg.Scanning.Timeout)

		// Test ArgoCD config
		assert.True(t, cfg.Scanning.ArgoCD.Enabled)
		assert.Equal(t, "custom-argocd", cfg.Scanning.ArgoCD.Namespace)
		assert.Equal(t, "clone", cfg.Scanning.ArgoCD.FetchMethod)

		// Test Flux config
		assert.True(t, cfg.Scanning.Flux.Enabled)
		assert.Equal(t, "custom-flux", cfg.Scanning.Flux.SystemNamespace)

		// Test cache config
		assert.False(t, cfg.Cache.Enabled)
		assert.Equal(t, 48*time.Hour, cfg.Cache.TTL)

		// Test output config
		assert.Equal(t, "json", cfg.Output.Format)
		assert.False(t, cfg.Output.ShowProgress)

		// Test logging config
		assert.Equal(t, "debug", cfg.Logging.Level)
		assert.Equal(t, "json", cfg.Logging.Format)
	})

	t.Run("environment variables override config", func(t *testing.T) {
		// Set environment variable
		os.Setenv("KUBUTO_ENGINE_URL", "https://env-engine.example.com")
		defer os.Unsetenv("KUBUTO_ENGINE_URL")

		cfg := LoadGlobal()
		assert.NotNil(t, cfg)
		assert.Equal(t, "https://env-engine.example.com", cfg.Engine.URL)
	})
}
