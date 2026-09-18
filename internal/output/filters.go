package output

import (
	"sort"
	"strings"

	"github.com/akshatsinha007/kubuto/pkg/types"
)

// FilterResources filters resources based on status filters and
// security-only flags. Each filter value accepts both the shorthand
// form ("major") and the full UpdateStatus name ("major-available") —
// callers never need to know which of the two the underlying status
// string actually uses.
func FilterResources(resources []types.Resource, filters []string, securityOnly bool) []types.Resource {
	filterSet := make(map[string]bool)
	for _, f := range filters {
		v := strings.ToLower(strings.TrimSpace(f))
		filterSet[v] = true
		// Guard against double-suffixing a value that already ends in
		// "-available" (e.g. "digest-update-available" itself).
		if !strings.HasSuffix(v, "-available") {
			filterSet[v+"-available"] = true
		}
	}

	hasFilters := len(filterSet) > 0

	var result []types.Resource
	for _, r := range resources {
		// If --security-only, only show resources with security risk
		if securityOnly && r.SecurityRisk == types.SecurityRiskNone {
			continue
		}

		// If specific filters are set, only include matching statuses
		if hasFilters {
			status := strings.ToLower(string(r.UpdateStatus))
			if !filterSet[status] {
				continue
			}
		}

		result = append(result, r)
	}

	return result
}

// FilterByDestination keeps only resources whose destination cluster
// matches dest, checked against either DestinationServer or
// DestinationNamespace (whichever the resource has set — ArgoCD
// Applications populate one or the other depending on whether
// spec.destination used .server/.name or a plain .namespace-only same-
// cluster reference). A blank dest is a no-op (returns resources
// unchanged), so callers can pass the raw --destination-cluster flag
// value without a separate "was it set" check. Kept as a standalone
// function rather than folded into FilterResources: that function's
// filter values are UpdateStatus shorthand strings with an
// auto-suffixing rule that would silently mangle a free-form value like
// a cluster name.
func FilterByDestination(resources []types.Resource, dest string) []types.Resource {
	if dest == "" {
		return resources
	}

	var result []types.Resource
	for _, r := range resources {
		if r.DestinationServer == dest || r.DestinationNamespace == dest {
			result = append(result, r)
		}
	}
	return result
}

// SortResources sorts resources by the specified field.
func SortResources(resources []types.Resource, sortBy string) []types.Resource {
	if len(resources) == 0 {
		return resources
	}

	sorted := make([]types.Resource, len(resources))
	copy(sorted, resources)

	switch strings.ToLower(sortBy) {
	case "name":
		sort.Slice(sorted, func(i, j int) bool {
			return sorted[i].Name < sorted[j].Name
		})
	case "namespace":
		sort.Slice(sorted, func(i, j int) bool {
			if sorted[i].Namespace == sorted[j].Namespace {
				return sorted[i].Name < sorted[j].Name
			}
			return sorted[i].Namespace < sorted[j].Namespace
		})
	case "status":
		sort.Slice(sorted, func(i, j int) bool {
			return statusPriority(sorted[i].UpdateStatus) < statusPriority(sorted[j].UpdateStatus)
		})
	case "risk":
		sort.Slice(sorted, func(i, j int) bool {
			return riskPriority(sorted[i].SecurityRisk) < riskPriority(sorted[j].SecurityRisk)
		})
	case "destination":
		sort.Slice(sorted, func(i, j int) bool {
			return DestinationDisplay(sorted[i]) < DestinationDisplay(sorted[j])
		})
	}

	return sorted
}

// statusPriority returns sort priority for update status (lower = more urgent).
func statusPriority(s types.UpdateStatus) int {
	switch s {
	case types.UpdateStatusMajor:
		return 0
	case types.UpdateStatusDeprecated:
		return 1
	case types.UpdateStatusMinor:
		return 2
	case types.UpdateStatusPatch:
		return 3
	case types.UpdateStatusDigestUpdateAvailable:
		return 4
	case types.UpdateStatusPinnedDigest:
		return 5
	case types.UpdateStatusCurrent:
		return 6
	default:
		return 7
	}
}

// DestinationDisplay returns the human-facing destination-cluster string
// for a resource: DestinationServer when set, falling back to
// DestinationNamespace, or "" for non-ArgoCD resources that never set
// either. Shared by both table renderers so the two stay consistent.
func DestinationDisplay(r types.Resource) string {
	if r.DestinationServer != "" {
		return r.DestinationServer
	}
	return r.DestinationNamespace
}

// riskPriority returns sort priority for security risk (lower = more urgent).
func riskPriority(r types.SecurityRisk) int {
	switch r {
	case types.SecurityRiskCritical:
		return 0
	case types.SecurityRiskHigh:
		return 1
	case types.SecurityRiskMedium:
		return 2
	case types.SecurityRiskLow:
		return 3
	default:
		return 4
	}
}
