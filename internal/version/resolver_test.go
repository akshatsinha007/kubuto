package version

import (
	"testing"

	"github.com/Masterminds/semver/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolve(t *testing.T) {
	// Test case setup
	current, err := semver.NewVersion("1.2.3")
	require.NoError(t, err)

	availableStrings := []string{
		"1.2.3",
		"1.2.4", // latest patch
		"1.3.0", // a minor update
		"1.3.1", // latest minor
		"2.0.0", // latest overall
		"1.1.0", // older
		"invalid-version",
		"2.1.0-beta", // pre-release, should be ignored by default
	}
	availableVers := StringsToVersions(availableStrings)

	// Expected results
	expectedLatest, _ := semver.NewVersion("2.0.0")
	expectedMinor, _ := semver.NewVersion("1.3.1")
	expectedPatch, _ := semver.NewVersion("1.2.4")

	// Run the resolver
	latest, latestMinor, latestPatch := Resolve(current, availableVers)

	// Assertions
	require.NotNil(t, latest)
	require.NotNil(t, latestMinor)
	require.NotNil(t, latestPatch)
	assert.Equal(t, expectedLatest.String(), latest.String(), "Latest version should be correct")
	assert.Equal(t, expectedMinor.String(), latestMinor.String(), "Latest minor version should be correct")
	assert.Equal(t, expectedPatch.String(), latestPatch.String(), "Latest patch version should be correct")
}

func TestResolve_NoUpdate(t *testing.T) {
	current, err := semver.NewVersion("2.0.0")
	require.NoError(t, err)

	availableStrings := []string{"1.0.0", "1.9.9", "2.0.0"}
	availableVers := StringsToVersions(availableStrings)

	latest, latestMinor, latestPatch := Resolve(current, availableVers)

	assert.Nil(t, latest, "Latest should be nil when no update is available")
	assert.Nil(t, latestMinor, "Latest minor should be nil when no update is available")
	assert.Nil(t, latestPatch, "Latest patch should be nil when no update is available")
}

func TestResolve_WithPrerelease(t *testing.T) {
	current, err := semver.NewVersion("2.0.0")
	require.NoError(t, err)

	// Pre-releases should be ignored if current is not a pre-release
	availableStrings := []string{"2.0.1-alpha.1", "2.1.0-beta"}
	availableVers := StringsToVersions(availableStrings)

	latest, _, _ := Resolve(current, availableVers)
	// The logic in Resolve filters pre-releases if current is stable.
	assert.Nil(t, latest, "Latest should be nil when only pre-releases are available for a stable version")

	// Now test when current is a pre-release, it should consider pre-release updates
	currentPre, err := semver.NewVersion("2.0.0-alpha.1")
	require.NoError(t, err)

	availableStringsWithPre := []string{"2.0.0-alpha.2", "2.0.0-beta.1", "2.0.0"}
	availableVersWithPre := StringsToVersions(availableStringsWithPre)
	// The stable version `2.0.0` is considered the latest version compared to a pre-release like `2.0.0-alpha.1`
	expectedLatestPre, _ := semver.NewVersion("2.0.0")

	latestPre, _, _ := Resolve(currentPre, availableVersWithPre)
	require.NotNil(t, latestPre)
	assert.Equal(t, expectedLatestPre.String(), latestPre.String(), "Should pick the final release as latest")
}

func TestStringsToVersions(t *testing.T) {
	input := []string{"1.0.0", "invalid", "1.2.3-alpha", "2.0.0"}
	versions := StringsToVersions(input)

	assert.Len(t, versions, 3, "Should parse 3 valid versions")
	assert.Equal(t, "1.0.0", versions[0].String())
	assert.Equal(t, "1.2.3-alpha", versions[1].String())
	assert.Equal(t, "2.0.0", versions[2].String())
}
