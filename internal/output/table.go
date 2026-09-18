package output

import (
	"os"

	"github.com/akshatsinha007/kubuto/pkg/types"
	"github.com/jedib0t/go-pretty/v6/table"
)

// RenderTable prints the scan results in a table format using go-pretty.
func RenderTable(result *types.ScanResult) {
	t := table.NewWriter()
	t.SetOutputMirror(os.Stdout)

	hasCompat, hasDestination := detectOptionalColumns(result.Resources)

	header := table.Row{"Name", "Namespace", "Type", "Current Version", "Latest", "Status"}
	if hasDestination {
		header = append(header, "Destination")
	}
	if hasCompat {
		header = append(header, "Compat", "Target Version", "Action")
	}
	t.AppendHeader(header)

	for _, r := range result.Resources {
		row := table.Row{
			r.Name,
			r.Namespace,
			string(r.Type),
			r.CurrentVersion,
			r.LatestVersion,
			string(r.UpdateStatus),
		}
		if hasDestination {
			row = append(row, DestinationDisplay(r))
		}
		if hasCompat {
			row = append(row, string(r.CompatStatus), r.TargetVersion, r.MigrationAction)
		}
		t.AppendRow(row)
	}

	t.Render()
}

// detectOptionalColumns reports whether any resource has compatibility
// data, and whether any resource has a destination cluster set
// (ArgoCD/Flux-sourced rows only) — each is its own optional column,
// gated independently so a plain `kubuto scan` (no ArgoCD, no engine)
// still gets the original 6-column table. Shared by both table
// renderers (RenderTable and renderTableText) so they can't drift apart
// on which columns appear when.
func detectOptionalColumns(resources []types.Resource) (hasCompat, hasDestination bool) {
	for _, r := range resources {
		if r.CompatStatus != "" {
			hasCompat = true
		}
		if r.DestinationServer != "" || r.DestinationNamespace != "" {
			hasDestination = true
		}
	}
	return hasCompat, hasDestination
}
