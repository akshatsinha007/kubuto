// Copyright 2024 The Kubuto Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

// Helm subchart enable/disable resolution.
//
// In Helm, an umbrella chart's `Chart.yaml` declares dependencies like:
//
//	dependencies:
//	  - name: minio
//	    version: 12.6.4
//	    repository: https://charts.bitnami.com/bitnami
//	    condition: minio.enabled
//	    tags: [storage, optional]
//
// At install time Helm renders ONLY the deps whose `condition` evaluates
// truthy in the merged values, OR whose `tags` intersect with the user's
// tag selection. If neither condition nor tags are set, the dep is
// always rendered (Helm default).
//
// Until now the scanner emitted one report row per `Chart.yaml` dep
// regardless — so a user who left `minio.enabled: false` would still see
// "minio: incompatible" verdicts in their scan output, which is both
// wrong and noisy. This file implements the same enable/disable rules
// Helm itself uses (sans the rarely-used `import-values` / `alias`
// rewrites, which don't change visibility).

package scanner

import (
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// isDepEnabled reports whether a Helm subchart dependency would actually
// be rendered, given the user's merged values map. The semantics mirror
// Helm's `processDependencyEnabled` in `pkg/chartutil/dependencies.go`:
//
//   - No `condition` AND no `tags`        → enabled (Helm default).
//   - `condition` is set, comma-separated → first key whose value EXISTS
//     in `values` decides. Truthy → enabled, falsy → disabled. If none
//     of the keys exist, fall through to tags.
//   - `tags` is set                       → if any tag matches a truthy
//     entry under `values["tags"][tag]`, enabled. If a tag exists in
//     values and is explicitly `false`, that wins over implicit-true
//     defaults.
//
// `values` is the parsed YAML/JSON values map that would be passed to
// `helm install -f`. Pass `nil` (or an empty map) to model "user
// supplied no values" — in that case condition keys are absent so we
// fall through to the Helm default of `enabled = true`, which matches
// how Helm itself behaves.
func isDepEnabled(condition, tags string, values map[string]any) bool {
	// Tags first: a tag that's been explicitly disabled overrides
	// everything else, mirroring Helm's behaviour.
	if tags != "" {
		if decided, enabled := evalTags(tags, values); decided {
			return enabled
		}
	}

	if condition == "" {
		// No condition AND tags didn't decide → Helm default.
		return true
	}

	// `condition` may list comma-separated paths; the first one that
	// exists in `values` wins (Helm checks them in order).
	for _, raw := range strings.Split(condition, ",") {
		key := strings.TrimSpace(raw)
		if key == "" {
			continue
		}
		if v, ok := lookupPath(values, key); ok {
			return truthy(v)
		}
	}
	// None of the condition paths exist → fall back to Helm default.
	return true
}

// evalTags returns (decided, enabled). `decided=false` means tags did
// not produce a verdict and the caller should fall through to condition.
func evalTags(tags string, values map[string]any) (bool, bool) {
	tagsMap, _ := lookupPath(values, "tags")
	tagsAsMap, _ := tagsMap.(map[string]any)

	anyTrue := false
	for _, raw := range strings.Split(tags, ",") {
		tag := strings.TrimSpace(raw)
		if tag == "" {
			continue
		}
		if v, ok := tagsAsMap[tag]; ok {
			if !truthy(v) {
				// Explicit false on any tag is a hard "no" in Helm.
				return true, false
			}
			anyTrue = true
		}
	}
	return anyTrue, anyTrue
}

// lookupPath walks a dotted path (`minio.enabled`) inside a nested
// values map. Returns (value, true) if every segment exists; (nil, false)
// otherwise. We deliberately do NOT support array indexing — Helm
// `condition` keys are documented as map paths only.
func lookupPath(values map[string]any, dotted string) (any, bool) {
	if values == nil || dotted == "" {
		return nil, false
	}
	parts := strings.Split(dotted, ".")
	var cur any = values
	for _, p := range parts {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = m[p]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

// truthy applies the same coercion rules Helm uses for condition / tag
// values: booleans pass through; strings "true"/"false" (case-insensitive)
// parse; numbers nonzero are true; anything else is treated as false to
// stay on the safe side (we'd rather under-report than show phantom
// rows for a chart the user didn't actually deploy).
func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		b, err := strconv.ParseBool(strings.TrimSpace(x))
		return err == nil && b
	case int:
		return x != 0
	case int32:
		return x != 0
	case int64:
		return x != 0
	case float32:
		return x != 0
	case float64:
		return x != 0
	default:
		return false
	}
}

// mergeValuesYAML parses a list of Helm values documents (each one a YAML
// string) and deep-merges them in order so later docs override earlier
// ones. This is how ArgoCD assembles `spec.source.helm.values` and
// `spec.source.helm.valuesObject` against the chart's defaults.
//
// Returns an empty (but non-nil) map when there is nothing to parse so
// callers can pass the result straight into isDepEnabled without nil-checks.
func mergeValuesYAML(docs ...string) (map[string]any, error) {
	out := map[string]any{}
	for _, doc := range docs {
		doc = strings.TrimSpace(doc)
		if doc == "" {
			continue
		}
		var parsed map[string]any
		if err := yaml.Unmarshal([]byte(doc), &parsed); err != nil {
			return out, err
		}
		out = mergeMaps(out, parsed)
	}
	return out, nil
}

// mergeMaps deep-merges `b` into `a`, with `b` winning on conflicts. We
// recurse into nested maps but otherwise replace wholesale (mirroring
// Helm's own merge semantics — arrays are NOT concatenated).
func mergeMaps(a, b map[string]any) map[string]any {
	if a == nil {
		a = map[string]any{}
	}
	for k, vb := range b {
		if va, ok := a[k]; ok {
			ma, aIsMap := va.(map[string]any)
			mb, bIsMap := vb.(map[string]any)
			if aIsMap && bIsMap {
				a[k] = mergeMaps(ma, mb)
				continue
			}
		}
		a[k] = vb
	}
	return a
}
