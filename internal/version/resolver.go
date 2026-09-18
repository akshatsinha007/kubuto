package version

import (
	"sort"

	"github.com/Masterminds/semver/v3"
)

// Resolve determines the latest, latest minor, and latest patch versions from a list of available versions.
// It assumes the availableVersions list contains valid semantic versions.
func Resolve(currentVersion *semver.Version, availableVersions []*semver.Version) (latest, latestMinor, latestPatch *semver.Version) {
	if len(availableVersions) == 0 {
		return
	}

	// Create a copy to avoid modifying the original slice
	sortedVersions := make([]*semver.Version, len(availableVersions))
	copy(sortedVersions, availableVersions)

	// Sort available versions in descending order to make resolution easier.
	sort.Sort(sort.Reverse(semver.Collection(sortedVersions)))

	// If no current version is provided (e.g., for a digest), the "latest" is the absolute latest non-prerelease version.
	if currentVersion == nil {
		for _, v := range sortedVersions {
			if v.Prerelease() == "" {
				latest = v
				return // We can't determine minor/patch, so we're done.
			}
		}
		// If only pre-releases exist, return the latest one.
		if latest == nil && len(sortedVersions) > 0 {
			latest = sortedVersions[0]
		}
		return
	}

	// The latest version is the first one in the sorted list that is greater than current.
	// We also check that it's not a pre-release, unless the current version is also a pre-release.
	for _, v := range sortedVersions {
		if v.GreaterThan(currentVersion) {
			if v.Prerelease() != "" && currentVersion.Prerelease() == "" {
				continue // Skip pre-release if current is not a pre-release
			}
			latest = v
			break
		}
	}

	// Find the latest patch (same major, same minor, greater patch)
	for _, v := range sortedVersions {
		if v.Major() == currentVersion.Major() &&
			v.Minor() == currentVersion.Minor() &&
			v.GreaterThan(currentVersion) {
			if v.Prerelease() != "" && currentVersion.Prerelease() == "" {
				continue
			}
			latestPatch = v
			break // Since it's sorted, the first one we find is the latest
		}
	}

	// Find the latest minor (same major, greater minor)
	for _, v := range sortedVersions {
		if v.Major() == currentVersion.Major() &&
			v.Minor() > currentVersion.Minor() {
			if v.Prerelease() != "" && currentVersion.Prerelease() == "" {
				continue
			}
			latestMinor = v
			break // Since it's sorted, the first one we find is the latest
		}
	}

	return
}

// StringsToVersions converts a slice of version strings to a slice of semver.Version objects.
// It ignores any strings that cannot be parsed as a valid semantic version.
func StringsToVersions(versionStrings []string) []*semver.Version {
	versions := make([]*semver.Version, 0, len(versionStrings))
	for _, vStr := range versionStrings {
		v, err := Parse(vStr)
		if err == nil {
			versions = append(versions, v)
		}
	}
	return versions
}
