// These tests exist because user feedback during the bug-fix session
// pointed out a real gap: previously the scanner emitted one row per
// Chart.yaml dependency regardless of whether the user had enabled the
// subchart via Helm `condition` / `tags` semantics. So an umbrella chart
// that bundles an OPTIONAL `minio` would incorrectly report "minio:
// incompatible" even when the user left `minio.enabled: false`.
//
// Each test below either documents one rule of Helm's actual
// dependency-resolution behaviour or guards the scanner against the
// noise it would otherwise produce. The intent is that a future
// maintainer reading these tests can immediately see *why* the rule
// matters, not just what it does.
package scanner

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsDepEnabled_DefaultIsTrue(t *testing.T) {
	// No `condition` and no `tags` mirrors plain `dependencies:` entries
	// in Chart.yaml — Helm always renders these. We must do the same to
	// avoid silently dropping required charts from the report.
	assert.True(t, isDepEnabled("", "", nil))
	assert.True(t, isDepEnabled("", "", map[string]any{"unrelated": true}))
}

func TestIsDepEnabled_ConditionTruthy(t *testing.T) {
	cases := []struct {
		name      string
		condition string
		values    map[string]any
		want      bool
	}{
		{
			name:      "explicit true bool enables",
			condition: "minio.enabled",
			values:    map[string]any{"minio": map[string]any{"enabled": true}},
			want:      true,
		},
		{
			name:      "explicit false bool disables — the user-feedback case",
			condition: "minio.enabled",
			values:    map[string]any{"minio": map[string]any{"enabled": false}},
			want:      false,
		},
		{
			name:      "string 'true' parses as truthy",
			condition: "minio.enabled",
			values:    map[string]any{"minio": map[string]any{"enabled": "true"}},
			want:      true,
		},
		{
			name:      "string 'false' parses as falsy",
			condition: "minio.enabled",
			values:    map[string]any{"minio": map[string]any{"enabled": "false"}},
			want:      false,
		},
		{
			name:      "non-zero number is truthy",
			condition: "feature.flag",
			values:    map[string]any{"feature": map[string]any{"flag": 1}},
			want:      true,
		},
		{
			name:      "zero is falsy",
			condition: "feature.flag",
			values:    map[string]any{"feature": map[string]any{"flag": 0}},
			want:      false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, isDepEnabled(tc.condition, "", tc.values))
		})
	}
}

func TestIsDepEnabled_ConditionAbsentFallsThrough(t *testing.T) {
	// If the condition path doesn't exist anywhere in the values, Helm
	// falls back to the dep's default-enabled behaviour. Without this
	// rule, a user who supplied no values at all would have *every*
	// optional dep marked disabled, which is the opposite of what Helm
	// actually does on `helm install`.
	values := map[string]any{
		"someOtherKey": true,
	}
	assert.True(t, isDepEnabled("missing.key", "", values))
}

func TestIsDepEnabled_ConditionCommaListFirstWins(t *testing.T) {
	// Helm allows `condition: a.enabled,b.enabled`; the FIRST path that
	// actually appears in values wins, even if a later path would have
	// flipped the verdict. We must match that exact precedence so the
	// scan output never disagrees with what Helm itself would render.
	values := map[string]any{
		"b": map[string]any{"enabled": true},
		"a": map[string]any{"enabled": false}, // first-listed, first-checked
	}
	assert.False(t, isDepEnabled("a.enabled,b.enabled", "", values))
}

func TestIsDepEnabled_TagsExplicitFalseWins(t *testing.T) {
	// In Helm, an explicit `tags.<x>: false` is a hard "no" — even if
	// another tag in the same dep is true, the dep is disabled. Locking
	// this rule prevents us from reporting a chart the user explicitly
	// turned off via tag selection.
	values := map[string]any{
		"tags": map[string]any{
			"storage":  false,
			"optional": true,
		},
	}
	assert.False(t, isDepEnabled("", "storage,optional", values))
}

func TestIsDepEnabled_TagsAnyTrueEnables(t *testing.T) {
	values := map[string]any{
		"tags": map[string]any{
			"storage": true,
		},
	}
	assert.True(t, isDepEnabled("", "storage,optional", values))
}

func TestIsDepEnabled_TagsAbsentFallsThroughToCondition(t *testing.T) {
	// Tags that don't appear in values must NOT pre-empt condition
	// evaluation; the test guards against accidentally short-circuiting
	// to "disabled" when the user hasn't expressed any tag preferences.
	values := map[string]any{
		"foo": map[string]any{"enabled": true},
	}
	assert.True(t, isDepEnabled("foo.enabled", "untouched-tag", values))
}

func TestMergeValuesYAML_LaterDocsOverride(t *testing.T) {
	// Helm's precedence: later -f files override earlier ones for the
	// same key. We replicate that for ArgoCD's `values` (string) followed
	// by `valuesObject`. The test guarantees a downstream override
	// actually wins — without this, a stale base config could enable a
	// subchart the user later disabled.
	merged, err := mergeValuesYAML(
		`minio:
  enabled: true`,
		`minio:
  enabled: false`,
	)
	assert.NoError(t, err)
	assert.Equal(t,
		map[string]any{"enabled": false},
		merged["minio"],
	)
}

func TestMergeValuesYAML_DeepMergeOnMaps(t *testing.T) {
	// Sibling keys under the same map should be preserved across merges.
	// A naive `b wins` strategy would clobber `minio.host` in the test
	// below — that bug would manifest as "the user set minio.host but
	// our condition lookup can't find it".
	merged, err := mergeValuesYAML(
		`minio:
  host: storage.example.com
  enabled: true`,
		`minio:
  enabled: false`,
	)
	assert.NoError(t, err)
	assert.Equal(t, map[string]any{
		"host":    "storage.example.com",
		"enabled": false,
	}, merged["minio"])
}

func TestMergeValuesYAML_EmptyAndInvalid(t *testing.T) {
	// Empty docs are skipped (common when a user only sets values via
	// `parameters`). Invalid YAML returns the partial map so the caller
	// can still proceed with a best-effort answer.
	merged, err := mergeValuesYAML("", "  ")
	assert.NoError(t, err)
	assert.Empty(t, merged)

	// `:` is invalid YAML at the top level.
	merged, err = mergeValuesYAML(": invalid")
	assert.Error(t, err)
	assert.NotNil(t, merged)
}

func TestSetDottedPath_CreatesIntermediateMaps(t *testing.T) {
	// ArgoCD passes parameter overrides as flat dotted keys. The path
	// builder must create the missing intermediate maps so condition
	// lookup finds the leaf — otherwise a `--parameter minio.enabled=false`
	// would never disable the subchart.
	values := map[string]any{}
	setDottedPath(values, "minio.enabled", "false")
	assert.False(t, isDepEnabled("minio.enabled", "", values))
}

func TestSetDottedPath_OverwritesScalarToMap(t *testing.T) {
	// If a scalar already lives at a partial path and a new override
	// requires turning that path into a map, we replace wholesale —
	// matching `helm install --set` behaviour, which discards the
	// scalar in favour of the map needed to reach the deeper key.
	values := map[string]any{"minio": "scalar"}
	setDottedPath(values, "minio.enabled", "true")
	got, ok := values["minio"].(map[string]any)
	assert.True(t, ok, "scalar must be replaced by a map")
	assert.Equal(t, "true", got["enabled"])
}
