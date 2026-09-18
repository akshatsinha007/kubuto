package version

import (
	"testing"

	"github.com/Masterminds/semver/v3"
	"github.com/akshatsinha007/kubuto/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompare(t *testing.T) {
	testCases := []struct {
		name     string
		current  string
		latest   string
		expected types.UpdateStatus
	}{
		{
			name:     "Major Update",
			current:  "1.5.0",
			latest:   "2.0.0",
			expected: types.UpdateStatusMajor,
		},
		{
			name:     "Minor Update",
			current:  "1.5.0",
			latest:   "1.6.0",
			expected: types.UpdateStatusMinor,
		},
		{
			name:     "Patch Update",
			current:  "1.5.0",
			latest:   "1.5.1",
			expected: types.UpdateStatusPatch,
		},
		{
			name:     "No Update (Equal)",
			current:  "1.5.0",
			latest:   "1.5.0",
			expected: types.UpdateStatusCurrent,
		},
		{
			name:     "No Update (Latest is nil)",
			current:  "1.5.0",
			latest:   "", // Will result in nil latest
			expected: types.UpdateStatusCurrent,
		},
		{
			name:     "Unknown (Latest is older)",
			current:  "1.5.0",
			latest:   "1.4.0",
			expected: types.UpdateStatusUnknown,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			currentVer, err := semver.NewVersion(tc.current)
			require.NoError(t, err)

			var latestVer *semver.Version
			if tc.latest != "" {
				latestVer, err = semver.NewVersion(tc.latest)
				require.NoError(t, err)
			}

			status := Compare(currentVer, latestVer)
			assert.Equal(t, tc.expected, status)
		})
	}
}
