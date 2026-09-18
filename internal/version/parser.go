package version

import (
	"github.com/Masterminds/semver/v3"
)

// Parse parses a version string and returns a semver object.
// It returns an error if the version string is not a valid semantic version.
func Parse(versionString string) (*semver.Version, error) {
	// NewVersion is a convenience function to parse a version string.
	// It returns an error if the version is not valid.
	return semver.NewVersion(versionString)
}
