package output

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/akshatsinha007/kubuto/pkg/types"
	"gopkg.in/yaml.v3"
)

// Format prints the scan results in the specified format.
func Format(result *types.ScanResult, format string) error {
	return FormatTo(result, format, "")
}

// FormatTo prints or writes scan results in the specified format.
// If exportPath is non-empty, writes to that file instead of stdout.
func FormatTo(result *types.ScanResult, format string, exportPath string) error {
	var output string
	var err error

	switch format {
	case "table":
		if exportPath != "" {
			// Table format to file — render as text
			output = renderTableText(result)
		} else {
			RenderTable(result)
			return nil
		}
	case "json":
		data, jerr := json.MarshalIndent(result, "", "  ")
		if jerr != nil {
			return fmt.Errorf("failed to marshal result to json: %w", jerr)
		}
		output = string(data)
	case "yaml":
		data, yerr := yaml.Marshal(result)
		if yerr != nil {
			return fmt.Errorf("failed to marshal result to yaml: %w", yerr)
		}
		output = string(data)
	default:
		return fmt.Errorf("unsupported output format: %s", format)
	}

	if exportPath != "" {
		err = os.WriteFile(exportPath, []byte(output), 0644)
		if err != nil {
			return fmt.Errorf("failed to write export file: %w", err)
		}
		fmt.Fprintf(os.Stderr, "Results exported to %s\n", exportPath)
	} else {
		fmt.Println(output)
	}

	return nil
}

// renderTableText renders scan results as a simple text table (for file export).
func renderTableText(result *types.ScanResult) string {
	var lines []string

	hasCompat, hasDestination := detectOptionalColumns(result.Resources)

	// Base columns are always present; Destination and the three Compat
	// columns are appended independently, matching table.go's gating so
	// the two renderers never drift apart on which columns appear when.
	headerFmt := "%-30s %-20s %-20s %-15s %-15s %-20s"
	headerArgs := []interface{}{"NAME", "NAMESPACE", "TYPE", "CURRENT", "LATEST", "STATUS"}
	rowFmt := "%-30s %-20s %-20s %-15s %-15s %-20s"

	if hasDestination {
		headerFmt += " %-25s"
		headerArgs = append(headerArgs, "DESTINATION")
		rowFmt += " %-25s"
	}
	if hasCompat {
		headerFmt += " %-15s %-15s %s"
		headerArgs = append(headerArgs, "COMPAT", "TARGET", "ACTION")
		rowFmt += " %-15s %-15s %s"
	}

	lines = append(lines, fmt.Sprintf(headerFmt, headerArgs...))
	for _, r := range result.Resources {
		rowArgs := []interface{}{r.Name, r.Namespace, string(r.Type), r.CurrentVersion, r.LatestVersion, string(r.UpdateStatus)}
		if hasDestination {
			rowArgs = append(rowArgs, DestinationDisplay(r))
		}
		if hasCompat {
			rowArgs = append(rowArgs, string(r.CompatStatus), r.TargetVersion, r.MigrationAction)
		}
		lines = append(lines, fmt.Sprintf(rowFmt, rowArgs...))
	}

	return fmt.Sprintf("%s\n", strings.Join(lines, "\n"))
}
