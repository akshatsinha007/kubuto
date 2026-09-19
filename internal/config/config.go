package config

import (
	"time"
)

type ClusterConfig struct {
	Timeout    time.Duration `mapstructure:"timeout" yaml:"timeout"`
	Kubeconfig string        `mapstructure:"kubeconfig" yaml:"kubeconfig"`
	Context    string        `mapstructure:"context" yaml:"context"`
}

type NamespacesConfig struct {
	Include []string `mapstructure:"include" yaml:"include"`
	Exclude []string `mapstructure:"exclude" yaml:"exclude"`
}

type GitOpsRepo struct {
	URL      string `mapstructure:"url" yaml:"url"`
	Revision string `mapstructure:"revision" yaml:"revision"`
	Path     string `mapstructure:"path" yaml:"path"`
	Token    string `mapstructure:"token" yaml:"token"`
	Username string `mapstructure:"username" yaml:"username"`
	Password string `mapstructure:"password" yaml:"password"`
	Repo     string `mapstructure:"repo" yaml:"repo"`
	Branch   string `mapstructure:"branch" yaml:"branch"`
}

type ScanningTypesConfig struct {
	Helm   bool `mapstructure:"helm" yaml:"helm"`
	Images bool `mapstructure:"images" yaml:"images"`
	ArgoCD bool `mapstructure:"argocd" yaml:"argocd"`
	Flux   bool `mapstructure:"flux" yaml:"flux"`
}

type HelmConfig struct {
	IncludePrivateRepos bool             `mapstructure:"include_private_repos" yaml:"include_private_repos"`
	Repositories        []string         `mapstructure:"repositories" yaml:"repositories"`
	CacheDuration       string           `mapstructure:"cache_duration" yaml:"cache_duration"`
	CustomRepos         []HelmRepoConfig `mapstructure:"custom_repos" yaml:"custom_repos"`
}

type HelmRepoConfig struct {
	Name string `mapstructure:"name" yaml:"name"`
	URL  string `mapstructure:"url" yaml:"url"`
}

type QuayIOConfig struct {
	Enabled bool `mapstructure:"enabled" yaml:"enabled"`
}

type GHCRConfig struct {
	Enabled bool `mapstructure:"enabled" yaml:"enabled"`
}

type ECRConfig struct {
	Enabled     bool   `mapstructure:"enabled" yaml:"enabled"`
	Region      string `mapstructure:"region" yaml:"region"`
	AccessKeyID string `mapstructure:"access_key_id" yaml:"access_key_id"`
}

type ImagesConfig struct {
	Enabled                  bool                  `mapstructure:"enabled" yaml:"enabled"`
	IncludePrivateRegistries bool                  `mapstructure:"include_private_registries" yaml:"include_private_registries"`
	Registries               ImageRegistriesConfig `mapstructure:"registries" yaml:"registries"`
}

type ImageRegistriesConfig struct {
	DockerHub DockerHubConfig `mapstructure:"dockerhub" yaml:"dockerhub"`
	QuayIO    QuayIOConfig    `mapstructure:"quayio" yaml:"quayio"`
	GHCR      GHCRConfig      `mapstructure:"ghcr" yaml:"ghcr"`
	ECR       ECRConfig       `mapstructure:"ecr" yaml:"ecr"`
}

type DockerHubConfig struct {
	Enabled   bool `mapstructure:"enabled" yaml:"enabled"`
	RateLimit int  `mapstructure:"rate_limit" yaml:"rate_limit"`
}

type ArgoCDConfig struct {
	// Enabled controls whether ArgoCD scanning is enabled by default
	// Can be overridden with --gitops argocd flag
	Enabled bool `mapstructure:"enabled" yaml:"enabled"`
	// Namespace where ArgoCD is installed (default: argocd)
	// Can be overridden with --gitops-namespace flag
	Namespace string `mapstructure:"namespace" yaml:"namespace"`
	// FetchMethod controls how Chart.yaml is fetched from git sources
	// "api" = use GitHub/GitLab API (fastest, default)
	// "clone" = shallow git clone (fallback for private repos)
	FetchMethod string `mapstructure:"fetch_method" yaml:"fetch_method"`
	// GitOps configs for accessing gitops repositories
	// Used to fetch Chart.yaml from git-based ArgoCD applications
	GitOps []GitOpsRepo `mapstructure:"gitops" yaml:"gitops"`
}

type FluxConfig struct {
	// Enabled controls whether Flux scanning is enabled by default
	// Can be overridden with --gitops flux flag
	Enabled bool `mapstructure:"enabled" yaml:"enabled"`
	// SystemNamespace where Flux controllers are installed (default: flux-system)
	SystemNamespace string `mapstructure:"system_namespace" yaml:"system_namespace"`
}

type ScanningConfig struct {
	Types       ScanningTypesConfig `mapstructure:"types" yaml:"types"`
	Concurrency int                 `mapstructure:"concurrency" yaml:"concurrency"`
	Timeout     time.Duration       `mapstructure:"timeout" yaml:"timeout"`
	BatchSize   int                 `mapstructure:"batch_size" yaml:"batch_size"`
	Helm        HelmConfig          `mapstructure:"helm" yaml:"helm"`
	Images      ImagesConfig        `mapstructure:"images" yaml:"images"`
	ArgoCD      ArgoCDConfig        `mapstructure:"argocd" yaml:"argocd"`
	Flux        FluxConfig          `mapstructure:"flux" yaml:"flux"`
	Namespaces  NamespacesConfig    `mapstructure:"namespaces" yaml:"namespaces"`
}

type EngineConfig struct {
	URL     string        `mapstructure:"url" yaml:"url"`
	Timeout time.Duration `mapstructure:"timeout" yaml:"timeout"`
	APIKey  string        `mapstructure:"api_key" yaml:"api_key"`
}

type CacheConfig struct {
	Enabled   bool          `mapstructure:"enabled" yaml:"enabled"`
	Directory string        `mapstructure:"directory" yaml:"directory"`
	TTL       time.Duration `mapstructure:"ttl" yaml:"ttl"`
	MaxSize   string        `mapstructure:"max_size" yaml:"max_size"`
}

type OutputConfig struct {
	Format       string `mapstructure:"format" yaml:"format"`
	ShowProgress bool   `mapstructure:"show_progress" yaml:"show_progress"`
	Colors       string `mapstructure:"colors" yaml:"colors"`
}

type FiltersConfig struct {
	UpdateStatus  []string `mapstructure:"update_status" yaml:"update_status"`
	SecurityRisk  []string `mapstructure:"security_risk" yaml:"security_risk"`
	ResourceTypes []string `mapstructure:"resource_types" yaml:"resource_types"`
	Namespaces    []string `mapstructure:"namespaces" yaml:"namespaces"`
}

type SortingConfig struct {
	Field string `mapstructure:"field" yaml:"field"`
	Order string `mapstructure:"order" yaml:"order"`
}

type LoggingConfig struct {
	Level  string `mapstructure:"level" yaml:"level"`
	Format string `mapstructure:"format" yaml:"format"`
}

type GitHubConfig struct {
	Token string `mapstructure:"token" yaml:"token"`
}

type Config struct {
	Cluster    ClusterConfig    `mapstructure:"cluster" yaml:"cluster"`
	Scanning   ScanningConfig   `mapstructure:"scanning" yaml:"scanning"`
	Engine     EngineConfig     `mapstructure:"engine" yaml:"engine"`
	Cache      CacheConfig      `mapstructure:"cache" yaml:"cache"`
	Output     OutputConfig     `mapstructure:"output" yaml:"output"`
	Filters    FiltersConfig    `mapstructure:"filters" yaml:"filters"`
	Sorting    SortingConfig    `mapstructure:"sorting" yaml:"sorting"`
	Logging    LoggingConfig    `mapstructure:"logging" yaml:"logging"`
	GitHub     GitHubConfig     `mapstructure:"github" yaml:"github"`
	Namespaces NamespacesConfig `mapstructure:"namespaces" yaml:"namespaces"`
}
