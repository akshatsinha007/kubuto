package utils

import (
	"fmt"
	"strings"
)

// ScanError represents an error that occurred during scanning a specific resource.
type ScanError struct {
	Resource  string
	Namespace string
	Err       error
}

// Error implements the error interface.
func (e *ScanError) Error() string {
	if e.Namespace != "" {
		return fmt.Sprintf("%s/%s: %v", e.Namespace, e.Resource, e.Err)
	}
	return fmt.Sprintf("%s: %v", e.Resource, e.Err)
}

// FormatErrors formats a slice of ScanErrors as a readable multi-line string.
func FormatErrors(errs []ScanError) string {
	if len(errs) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Encountered %d error(s):\n", len(errs)))
	for i, e := range errs {
		sb.WriteString(fmt.Sprintf("  %d. %s\n", i+1, e.Error()))
	}
	return sb.String()
}
