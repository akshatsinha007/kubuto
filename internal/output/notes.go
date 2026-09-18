package output

import (
	"fmt"
	"strings"

	"github.com/akshatsinha007/kubuto/pkg/types"
)

// TransparencyNotes summarizes known display limitations across a
// resource set. Two cases look like missing/broken data but are actually
// documented, intentional limitations:
//
//   - An OCI-sourced chart (Repository starting with "oci://") with a
//     blank LatestVersion: ResolveLatestFromIndex is a deliberate no-op
//     for OCI repos (index.yaml doesn't exist there), not a failed
//     lookup.
//   - A CurrentVersion pinned to a git revision ("git@<sha>", set by
//     internal/scanner/argocd_git_auth.go's gitRevisionLabel): the
//     source wasn't a resolvable Helm chart (Kustomize, a CMP plugin, or
//     a fetch failure), so there's no real chart version to report.
//
// Returns one note per condition that actually occurs (empty slice when
// neither applies), so a normal scan's output is unchanged.
func TransparencyNotes(resources []types.Resource) []string {
	var ociCount, gitPinCount int
	for _, r := range resources {
		if strings.HasPrefix(r.Repository, "oci://") && r.LatestVersion == "" {
			ociCount++
		}
		if strings.HasPrefix(r.CurrentVersion, "git@") {
			gitPinCount++
		}
	}

	var notes []string
	if ociCount > 0 {
		notes = append(notes, fmt.Sprintf(
			"%d chart(s) sourced from OCI registries — latest-version lookup isn't supported for OCI repos yet.",
			ociCount))
	}
	if gitPinCount > 0 {
		notes = append(notes, fmt.Sprintf(
			"%d chart(s) pinned to a git revision, not a real chart version (non-Helm or unresolvable source).",
			gitPinCount))
	}
	return notes
}

// PrintTransparencyNotes prints each note from TransparencyNotes to
// stdout, one per line. Intended to run once, right after table output,
// so a documented limitation isn't mistaken for missing/broken data.
func PrintTransparencyNotes(resources []types.Resource) {
	for _, note := range TransparencyNotes(resources) {
		fmt.Printf("ℹ️  %s\n", note)
	}
}
