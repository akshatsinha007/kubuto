package types

import "time"

// Resource represents any scannable Kubernetes resource
type Resource struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	Namespace      string            `json:"namespace"`
	Type           ResourceType      `json:"type"`
	CurrentVersion string            `json:"current_version"`
	LatestVersion  string            `json:"latest_version"`
	LatestMinor    string            `json:"latest_minor"`
	LatestPatch    string            `json:"latest_patch"`
	Repository     string            `json:"repository"`
	UpdateStatus   UpdateStatus      `json:"update_status"`
	SecurityRisk   SecurityRisk      `json:"security_risk"`
	Labels         map[string]string `json:"labels"`
	Annotations    map[string]string `json:"annotations"`
	LastChecked    time.Time         `json:"last_checked"`
	Confidence     float64           `json:"confidence"`

	// Compatibility fields (populated when engine is available)
	CompatStatus    CompatStatus `json:"compat_status,omitempty"`
	TargetVersion   string       `json:"target_version,omitempty"`
	MigrationAction string       `json:"migration_action,omitempty"`

	// CompatStale propagates the engine's `is_stale` flag end-to-end so
	// users can tell when a "compatible" verdict was based on a row the
	// engine has not re-scraped recently. Output formatters should render
	// a warning marker when this is true.
	CompatStale bool `json:"compat_stale,omitempty"`
	// CompatSource carries the engine's source URL (vendor docs / release
	// notes) for the verdict, so users can verify the answer themselves.
	CompatSource string `json:"compat_source,omitempty"`
	// CompatError captures any transport / parsing error returned by the
	// engine for this resource. The scan as a whole still succeeds; we
	// just couldn't classify this one row. Helpful for debugging in CI.
	CompatError string `json:"compat_error,omitempty"`

	// DestinationServer/DestinationNamespace identify which cluster this
	// resource is actually deployed to, for ArgoCD-sourced rows where the
	// Application CR (and thus `Namespace` above) lives in the ArgoCD
	// control-plane cluster, not necessarily the same cluster the
	// workload runs on. ArgoCD Applications set spec.destination.server
	// XOR spec.destination.name (rarely both) — DestinationServer holds
	// whichever one the Application specified. Empty for non-ArgoCD
	// resource types.
	DestinationServer    string `json:"destination_server,omitempty"`
	DestinationNamespace string `json:"destination_namespace,omitempty"`

	// GeneratedBy names the ArgoCD ApplicationSet that generated this
	// resource's Application (via an ownerReference of Kind
	// "ApplicationSet"), when one exists. Deliberately JSON/export-only —
	// not rendered as a table column — to avoid cluttering the default
	// table view for the common case (most Applications aren't
	// ApplicationSet-generated). Empty for non-ArgoCD resource types and
	// for standalone Applications.
	GeneratedBy string `json:"generated_by,omitempty"`
}

type ResourceType string

const (
	ResourceTypeHelmChart   ResourceType = "helm-chart"
	ResourceTypeImage       ResourceType = "container-image"
	ResourceTypeArgoApp     ResourceType = "argocd-application"
	ResourceTypeFluxRelease ResourceType = "flux-helmrelease"
	ResourceTypeFluxRepo    ResourceType = "flux-gitrepository"
)

type UpdateStatus string

const (
	UpdateStatusCurrent               UpdateStatus = "current"
	UpdateStatusPatch                 UpdateStatus = "patch-available"
	UpdateStatusMinor                 UpdateStatus = "minor-available"
	UpdateStatusMajor                 UpdateStatus = "major-available"
	UpdateStatusUnknown               UpdateStatus = "unknown"
	UpdateStatusDeprecated            UpdateStatus = "deprecated"
	UpdateStatusDigestUpdateAvailable UpdateStatus = "digest-update-available"
	UpdateStatusPinnedDigest          UpdateStatus = "pinned-digest"
)

type SecurityRisk string

const (
	SecurityRiskNone     SecurityRisk = "none"
	SecurityRiskLow      SecurityRisk = "low"
	SecurityRiskMedium   SecurityRisk = "medium"
	SecurityRiskHigh     SecurityRisk = "high"
	SecurityRiskCritical SecurityRisk = "critical"
)

type CompatStatus string

const (
	CompatStatusCompatible   CompatStatus = "compatible"
	CompatStatusIncompatible CompatStatus = "incompatible"
	CompatStatusDeprecated   CompatStatus = "deprecated"
	CompatStatusUnknown      CompatStatus = "unknown"
	CompatStatusNeedsGitOps  CompatStatus = "needs-gitops"
)
