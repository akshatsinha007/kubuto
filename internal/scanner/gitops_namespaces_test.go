package scanner

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestResolveTargetNamespaces locks down the namespace-resolution
// precedence shared by the ArgoCD and Flux scanners.
func TestResolveTargetNamespaces(t *testing.T) {
	cases := []struct {
		name string
		opts ScanOptions
		want []string
	}{
		{
			name: "no flags returns single empty-string sentinel for cluster-wide",
			opts: ScanOptions{},
			want: []string{""},
		},
		{
			name: "GitOpsNamespace alone wins over an empty Namespaces slice",
			opts: ScanOptions{GitOpsNamespace: "argocd"},
			want: []string{"argocd"},
		},
		{
			name: "GitOpsNamespace beats multi-NS Namespaces",
			opts: ScanOptions{
				GitOpsNamespace: "argocd",
				Namespaces:      []string{"foo", "bar"},
			},
			want: []string{"argocd"},
		},
		{
			name: "multi-NS Namespaces is honoured when GitOpsNamespace is empty",
			opts: ScanOptions{Namespaces: []string{"devtroncd", "default"}},
			want: []string{"devtroncd", "default"},
		},
		{
			name: "single-element Namespaces still walks just that namespace",
			opts: ScanOptions{Namespaces: []string{"automation"}},
			want: []string{"automation"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, resolveTargetNamespaces(tc.opts))
		})
	}
}

// TestResolveTargetNamespacesIsolation verifies the defensive copy
// protects ScanOptions.Namespaces from downstream mutation.
func TestResolveTargetNamespacesIsolation(t *testing.T) {
	src := []string{"a", "b"}
	opts := ScanOptions{Namespaces: src}

	out := resolveTargetNamespaces(opts)
	out[0] = "MUTATED"

	assert.Equal(t, "a", src[0],
		"resolveTargetNamespaces must return a copy; mutating the result must not bleed into ScanOptions")
}

// TestScopeForLog locks down the human-readable label flux.go writes
// to its V(1) logs when no namespace is constraining the list. We pin
// the exact string because grep-pattern dashboards/alerts may depend
// on the literal "cluster-wide".
func TestScopeForLog(t *testing.T) {
	assert.Equal(t, "cluster-wide", scopeForLog(""))
	assert.Equal(t, "devtroncd", scopeForLog("devtroncd"))
}
