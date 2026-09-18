// Copyright 2024 The Kubuto Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

// TestMatchRepoSecret_PrefixBoundary and TestHasURLPrefix guard
// matchRepoSecret's path-boundary matching in argocd.go.
package scanner

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMatchRepoSecret_PrefixBoundary(t *testing.T) {
	// The bug we are guarding against: `https://charts.example.com/foo`
	// must NOT match `https://charts.example.com/foobar` even though
	// strings.Contains would say they overlap. We also confirm that a
	// proper path-prefix DOES still match (path-within-repo case).
	secrets := []repoSecret{
		{URL: "https://charts.example.com/foobar", Type: "helm"},
		{URL: "https://charts.example.com/foo", Type: "helm"},
		{URL: "https://other.example.com/", Type: "helm"},
	}

	t.Run("path-boundary prefix match returns the proper repo", func(t *testing.T) {
		got := matchRepoSecret("https://charts.example.com/foo/subchart", secrets)
		assert.Equal(t, "https://charts.example.com/foo", got)
	})

	t.Run("non-boundary substring is rejected", func(t *testing.T) {
		// `foob` is a substring of `foobar` and shares the prefix `foo`,
		// but neither is a path-prefix of the other (no '/' boundary).
		// The old strings.Contains-based code would have matched both
		// and returned a wrong upstream; the new code correctly returns
		// no match — the user can then fall back to the raw URL.
		got := matchRepoSecret("https://charts.example.com/foob", secrets)
		assert.Empty(t, got,
			"non-boundary substring overlap must NOT match — that was the bug")
	})

	t.Run("exact match wins", func(t *testing.T) {
		got := matchRepoSecret("https://other.example.com/", secrets)
		assert.Equal(t, "https://other.example.com/", got)
	})

	t.Run("empty input returns empty", func(t *testing.T) {
		assert.Empty(t, matchRepoSecret("", secrets))
	})

	t.Run("no match returns empty", func(t *testing.T) {
		assert.Empty(t, matchRepoSecret("https://elsewhere.io/x", secrets))
	})
}

func TestHasURLPrefix(t *testing.T) {
	cases := []struct {
		longer, shorter string
		want            bool
	}{
		{"a/b/c", "a/b", true},   // proper path prefix
		{"a/b", "a/b", true},     // identical
		{"a/bc", "a/b", false},   // not on a path boundary
		{"a/b", "a/b/c", false},  // shorter is actually longer
		{"", "a", false},         // empty longer
		{"a", "", false},         // empty shorter
		{"abcdef", "abc", false}, // not a /-bounded prefix
	}
	for _, tc := range cases {
		t.Run(tc.longer+"|"+tc.shorter, func(t *testing.T) {
			assert.Equal(t, tc.want, hasURLPrefix(tc.longer, tc.shorter))
		})
	}
}
