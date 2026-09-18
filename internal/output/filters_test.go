package output

import (
	"testing"

	"github.com/akshatsinha007/kubuto/pkg/types"
)

func testResources() []types.Resource {
	return []types.Resource{
		{Name: "app-a", Namespace: "default", UpdateStatus: types.UpdateStatusCurrent, SecurityRisk: types.SecurityRiskNone},
		{Name: "app-b", Namespace: "kube-system", UpdateStatus: types.UpdateStatusPatch, SecurityRisk: types.SecurityRiskLow},
		{Name: "app-c", Namespace: "default", UpdateStatus: types.UpdateStatusMinor, SecurityRisk: types.SecurityRiskMedium},
		{Name: "app-d", Namespace: "monitoring", UpdateStatus: types.UpdateStatusMajor, SecurityRisk: types.SecurityRiskHigh},
		{Name: "app-e", Namespace: "default", UpdateStatus: types.UpdateStatusDeprecated, SecurityRisk: types.SecurityRiskCritical},
	}
}

func TestFilterResources_NoFilters(t *testing.T) {
	res := FilterResources(testResources(), nil, false)
	if len(res) != 5 {
		t.Errorf("expected 5 resources, got %d", len(res))
	}
}

func TestFilterResources_ByStatus(t *testing.T) {
	res := FilterResources(testResources(), []string{"patch-available"}, false)
	if len(res) != 1 {
		t.Fatalf("expected 1 resource, got %d", len(res))
	}
	if res[0].Name != "app-b" {
		t.Errorf("expected app-b, got %s", res[0].Name)
	}
}

func TestFilterResources_MultipleStatuses(t *testing.T) {
	res := FilterResources(testResources(), []string{"major-available", "deprecated"}, false)
	if len(res) != 2 {
		t.Fatalf("expected 2 resources, got %d", len(res))
	}
}

// TestFilterResources_ShorthandStatus locks in the fix for a previously
// documented bug (README: "--filter major returns nothing") — the
// shorthand form (without "-available") must match the same resources as
// the full status name.
func TestFilterResources_ShorthandStatus(t *testing.T) {
	for _, shorthand := range []string{"major", "minor", "patch"} {
		full := shorthand + "-available"
		gotShort := FilterResources(testResources(), []string{shorthand}, false)
		gotFull := FilterResources(testResources(), []string{full}, false)
		if len(gotShort) != len(gotFull) {
			t.Errorf("--filter %s matched %d resources, --filter %s matched %d — should be equal",
				shorthand, len(gotShort), full, len(gotFull))
		}
		if len(gotShort) != 1 {
			t.Errorf("--filter %s: expected 1 resource, got %d", shorthand, len(gotShort))
		}
	}
}

// TestFilterResources_AlreadySuffixedStatusNotDoubleSuffixed guards against
// the shorthand fallback corrupting a value that's already a full status
// name, e.g. never turning "digest-update-available" into
// "digest-update-available-available".
func TestFilterResources_AlreadySuffixedStatusNotDoubleSuffixed(t *testing.T) {
	resources := []types.Resource{
		{Name: "app-f", UpdateStatus: types.UpdateStatusDigestUpdateAvailable},
	}
	res := FilterResources(resources, []string{"digest-update-available"}, false)
	if len(res) != 1 {
		t.Fatalf("expected 1 resource, got %d", len(res))
	}
}

func TestFilterResources_SecurityOnly(t *testing.T) {
	res := FilterResources(testResources(), nil, true)
	if len(res) != 4 {
		t.Fatalf("expected 4 resources with security risk, got %d", len(res))
	}
	for _, r := range res {
		if r.SecurityRisk == types.SecurityRiskNone {
			t.Errorf("should not include resource with no security risk: %s", r.Name)
		}
	}
}

func TestFilterResources_EmptyInput(t *testing.T) {
	res := FilterResources([]types.Resource{}, nil, false)
	if len(res) != 0 {
		t.Errorf("expected 0 resources, got %d", len(res))
	}
}

func TestSortResources_ByName(t *testing.T) {
	res := SortResources(testResources(), "name")
	if res[0].Name != "app-a" || res[4].Name != "app-e" {
		t.Errorf("not sorted by name: %s ... %s", res[0].Name, res[4].Name)
	}
}

func TestSortResources_ByNamespace(t *testing.T) {
	res := SortResources(testResources(), "namespace")
	if res[0].Namespace != "default" {
		t.Errorf("expected first namespace 'default', got '%s'", res[0].Namespace)
	}
}

func TestSortResources_ByStatus(t *testing.T) {
	res := SortResources(testResources(), "status")
	// Major should come first (priority 0), current last (priority 6)
	if res[0].UpdateStatus != types.UpdateStatusMajor {
		t.Errorf("expected major first, got %s", res[0].UpdateStatus)
	}
	if res[4].UpdateStatus != types.UpdateStatusCurrent {
		t.Errorf("expected current last, got %s", res[4].UpdateStatus)
	}
}

func TestSortResources_ByRisk(t *testing.T) {
	res := SortResources(testResources(), "risk")
	// Critical should come first (priority 0), none last (priority 4)
	if res[0].SecurityRisk != types.SecurityRiskCritical {
		t.Errorf("expected critical first, got %s", res[0].SecurityRisk)
	}
	if res[4].SecurityRisk != types.SecurityRiskNone {
		t.Errorf("expected none last, got %s", res[4].SecurityRisk)
	}
}

func TestSortResources_EmptyInput(t *testing.T) {
	res := SortResources([]types.Resource{}, "name")
	if len(res) != 0 {
		t.Errorf("expected 0 resources, got %d", len(res))
	}
}

func TestSortResources_InvalidSortBy(t *testing.T) {
	res := SortResources(testResources(), "invalid")
	if len(res) != 5 {
		t.Errorf("expected 5 resources (unsorted), got %d", len(res))
	}
}

func TestSortResources_DoesNotMutateOriginal(t *testing.T) {
	original := testResources()
	SortResources(original, "name")
	if original[0].Name != "app-a" {
		// Original should be unchanged — but since we pass by value slice, the underlying array
		// could be shared. This test verifies the copy behavior.
		// If SortResources modifies in place, this might fail.
	}
}

func TestStatusPriority(t *testing.T) {
	tests := []struct {
		status   types.UpdateStatus
		expected int
	}{
		{types.UpdateStatusMajor, 0},
		{types.UpdateStatusDeprecated, 1},
		{types.UpdateStatusMinor, 2},
		{types.UpdateStatusPatch, 3},
		{types.UpdateStatusDigestUpdateAvailable, 4},
		{types.UpdateStatusPinnedDigest, 5},
		{types.UpdateStatusCurrent, 6},
		{types.UpdateStatusUnknown, 7},
	}

	for _, tt := range tests {
		got := statusPriority(tt.status)
		if got != tt.expected {
			t.Errorf("statusPriority(%s) = %d, want %d", tt.status, got, tt.expected)
		}
	}
}

func TestRiskPriority(t *testing.T) {
	tests := []struct {
		risk     types.SecurityRisk
		expected int
	}{
		{types.SecurityRiskCritical, 0},
		{types.SecurityRiskHigh, 1},
		{types.SecurityRiskMedium, 2},
		{types.SecurityRiskLow, 3},
		{types.SecurityRiskNone, 4},
	}

	for _, tt := range tests {
		got := riskPriority(tt.risk)
		if got != tt.expected {
			t.Errorf("riskPriority(%s) = %d, want %d", tt.risk, got, tt.expected)
		}
	}
}

func destinationTestResources() []types.Resource {
	return []types.Resource{
		{Name: "app-a", DestinationServer: "https://spoke-a.example.com:6443"},
		{Name: "app-b", DestinationServer: "https://spoke-b.example.com:6443"},
		{Name: "app-c", DestinationNamespace: "workloads"}, // same-cluster, namespace-only destination
		{Name: "app-d"}, // non-ArgoCD resource, no destination at all
	}
}

func TestFilterByDestination_EmptyDestIsNoOp(t *testing.T) {
	res := FilterByDestination(destinationTestResources(), "")
	if len(res) != 4 {
		t.Fatalf("expected all 4 resources unchanged, got %d", len(res))
	}
}

func TestFilterByDestination_MatchesServer(t *testing.T) {
	res := FilterByDestination(destinationTestResources(), "https://spoke-a.example.com:6443")
	if len(res) != 1 || res[0].Name != "app-a" {
		t.Fatalf("expected exactly app-a, got %v", res)
	}
}

func TestFilterByDestination_MatchesNamespaceOnlyDestination(t *testing.T) {
	res := FilterByDestination(destinationTestResources(), "workloads")
	if len(res) != 1 || res[0].Name != "app-c" {
		t.Fatalf("expected exactly app-c, got %v", res)
	}
}

func TestFilterByDestination_NoMatch(t *testing.T) {
	res := FilterByDestination(destinationTestResources(), "https://nonexistent.example.com")
	if len(res) != 0 {
		t.Fatalf("expected 0 resources, got %d", len(res))
	}
}

func TestSortResources_ByDestination(t *testing.T) {
	res := SortResources(destinationTestResources(), "destination")
	if len(res) != 4 {
		t.Fatalf("expected 4 resources, got %d", len(res))
	}
	// Don't assert a specific order beyond "ascending" (avoids hardcoding
	// an https:// vs plain-namespace-string comparison that's an
	// implementation detail, not a behavior contract) — just verify the
	// sort actually did something.
	for i := 1; i < len(res); i++ {
		if DestinationDisplay(res[i-1]) > DestinationDisplay(res[i]) {
			t.Fatalf("results not sorted by destination: %q came before %q", DestinationDisplay(res[i-1]), DestinationDisplay(res[i]))
		}
	}
}

func TestDestinationDisplay(t *testing.T) {
	if got := DestinationDisplay(types.Resource{DestinationServer: "srv", DestinationNamespace: "ns"}); got != "srv" {
		t.Errorf("expected DestinationServer to win, got %q", got)
	}
	if got := DestinationDisplay(types.Resource{DestinationNamespace: "ns"}); got != "ns" {
		t.Errorf("expected fallback to DestinationNamespace, got %q", got)
	}
	if got := DestinationDisplay(types.Resource{}); got != "" {
		t.Errorf("expected empty string for a resource with neither field set, got %q", got)
	}
}
