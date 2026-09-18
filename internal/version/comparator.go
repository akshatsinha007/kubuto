package version

import (
	"github.com/Masterminds/semver/v3"
	"github.com/akshatsinha007/kubuto/pkg/types"
)

// Compare determines the update status by comparing the current version to the latest version.
func Compare(current, latest *semver.Version) types.UpdateStatus {
	if latest == nil || current.Equal(latest) {
		return types.UpdateStatusCurrent
	}

	if latest.GreaterThan(current) {
		if latest.Major() > current.Major() {
			return types.UpdateStatusMajor
		}
		if latest.Minor() > current.Minor() {
			return types.UpdateStatusMinor
		}
		if latest.Patch() > current.Patch() {
			return types.UpdateStatusPatch
		}
	}

	// This case could be for pre-releases or other edge cases, but for now, we'll consider it unknown.
	// Or if somehow latest is less than current, which shouldn't happen with proper resolver logic.
	return types.UpdateStatusUnknown
}
