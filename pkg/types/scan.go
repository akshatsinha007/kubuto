package types

import "time"

// ScanResult encapsulates the complete scan operation result
type ScanResult struct {
	Metadata   ScanMetadata `json:"metadata"`
	Resources  []Resource   `json:"resources"`
	Statistics ScanStats    `json:"statistics"`
	Errors     []ScanError  `json:"errors,omitempty"`
}

// ScanMetadata contains metadata about a scan.
type ScanMetadata struct {
	Timestamp     time.Time `json:"timestamp"`
	Duration      string    `json:"duration"`
	ClusterName   string    `json:"cluster_name"`
	KubutoVersion string    `json:"kubuto_version"`
	Namespaces    []string  `json:"namespaces"`
	ScanTypes     []string  `json:"scan_types"`
}

// ScanStats contains statistics about a scan.
type ScanStats struct {
	TotalResources   int `json:"total_resources"`
	CurrentResources int `json:"current_resources"`
	UpdatesAvailable int `json:"updates_available"`
	SecurityIssues   int `json:"security_issues"`
	FailedScans      int `json:"failed_scans"`
}

// ScanError represents an error that occurred during a scan.
type ScanError struct {
	Message string `json:"message"`
	Err     error  `json:"-"` // The actual error, not marshalled
}
