// Package config provides configuration structures for the Kubuto CLI
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// GetDefaultConfig returns the default configuration for the CLI
func GetDefaultConfig() *Config {
	return &Config{
		Cluster: ClusterConfig{
			Timeout:    30 * time.Second,
			Kubeconfig: "",
		},
		Scanning: ScanningConfig{
			Types: ScanningTypesConfig{
				Helm:   true,
				Images: true,
				ArgoCD: true,
				Flux:   true,
			},
			Concurrency: 10,
			Timeout:     60 * time.Second,
			BatchSize:   5,
			Helm: HelmConfig{
				IncludePrivateRepos: false,
			},
			Images: ImagesConfig{
				IncludePrivateRegistries: false,
				Registries: ImageRegistriesConfig{
					DockerHub: DockerHubConfig{
						Enabled:   true,
						RateLimit: 100,
					},
					QuayIO: QuayIOConfig{
						Enabled: true,
					},
				},
			},
			ArgoCD: ArgoCDConfig{
				Enabled:     false, // Disabled by default, enable with --gitops argocd
				Namespace:   "argocd",
				FetchMethod: "api", // Use API by default, fallback to clone
			},
			Flux: FluxConfig{
				Enabled:         false, // Disabled by default, enable with --gitops flux
				SystemNamespace: "flux-system",
			},
		},
		Engine: EngineConfig{
			URL:     "http://localhost:8080",
			Timeout: 30 * time.Second,
		},
		Cache: CacheConfig{
			Enabled:   true,
			Directory: "~/.kubuto/cache",
			TTL:       24 * time.Hour,
			MaxSize:   "500MB",
		},
		Output: OutputConfig{
			Format:       "table",
			ShowProgress: true,
			Colors:       "auto",
		},
		Filters: FiltersConfig{
			UpdateStatus:  []string{},
			SecurityRisk:  []string{},
			ResourceTypes: []string{},
			Namespaces:    []string{},
		},
		Sorting: SortingConfig{
			Field: "name",
			Order: "asc",
		},
		Logging: LoggingConfig{
			Level:  "info",
			Format: "text",
		},
	}
}

// LoadGlobal loads config from default locations with environment variable overrides
func LoadGlobal() *Config {
	cfg, err := LoadConfig("")
	if err != nil {
		return GetDefaultConfig()
	}
	return cfg
}

// LoadConfig loads config from file with the following precedence:
// 1. Command-line flags (handled by cobra)
// 2. Environment variables (KUBUTO_*)
// 3. Config file (./kubuto.yaml, $HOME/.kubuto/config.yaml, $HOME/.kubuto.yaml)
// 4. Default values
func LoadConfig(path string) (*Config, error) {
	v := viper.New()

	// Set defaults
	defaults := GetDefaultConfig()
	v.SetDefault("cluster.timeout", defaults.Cluster.Timeout)
	v.SetDefault("engine.url", defaults.Engine.URL)
	v.SetDefault("engine.timeout", defaults.Engine.Timeout)
	// engine.api_key has no config-file default (it's a secret, always
	// empty out of the box) — but it still needs an explicit SetDefault so
	// Viper's AutomaticEnv can resolve KUBUTO_ENGINE_API_KEY at all.
	// Without a registered default (or a BindEnv), Unmarshal never looks
	// for an env var on a key it doesn't already know exists, so the
	// documented override silently does nothing.
	v.SetDefault("engine.api_key", defaults.Engine.APIKey)
	v.SetDefault("scanning.concurrency", defaults.Scanning.Concurrency)
	v.SetDefault("scanning.timeout", defaults.Scanning.Timeout)
	v.SetDefault("scanning.argocd.enabled", defaults.Scanning.ArgoCD.Enabled)
	v.SetDefault("scanning.argocd.namespace", defaults.Scanning.ArgoCD.Namespace)
	v.SetDefault("scanning.argocd.fetch_method", defaults.Scanning.ArgoCD.FetchMethod)
	v.SetDefault("scanning.flux.enabled", defaults.Scanning.Flux.Enabled)
	v.SetDefault("scanning.flux.system_namespace", defaults.Scanning.Flux.SystemNamespace)
	v.SetDefault("cache.enabled", defaults.Cache.Enabled)
	v.SetDefault("cache.directory", defaults.Cache.Directory)
	v.SetDefault("cache.ttl", defaults.Cache.TTL)
	v.SetDefault("output.format", defaults.Output.Format)
	v.SetDefault("logging.level", defaults.Logging.Level)
	v.SetDefault("logging.format", defaults.Logging.Format)

	// Set config file path
	if path != "" {
		v.SetConfigFile(path)
	} else {
		// Search in current directory and home directory
		v.SetConfigName("config")
		v.AddConfigPath(".")

		homeDir, err := os.UserHomeDir()
		if err == nil {
			v.AddConfigPath(filepath.Join(homeDir, ".kubuto"))
			v.AddConfigPath(homeDir)
		}
	}

	v.SetConfigType("yaml")

	// Read config file (ignore error if file doesn't exist)
	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			// Config file exists but has errors
			return nil, fmt.Errorf("failed to read config file: %w", err)
		}
		// Config file not found, continue with defaults and env vars
	}

	// Bind environment variables
	v.SetEnvPrefix("KUBUTO")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	// Unmarshal into Config struct
	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	return &cfg, nil
}
