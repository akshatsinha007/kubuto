package cmd

import (
	"testing"

	"github.com/akshatsinha007/kubuto/internal/scanner"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
)

// resolveScanFlags is a pure function that mirrors the flag-resolution
// branching inside `scanCmd.RunE`. We extract it for the test so we can
// table-drive every (--type, --gitops, --all) combination without
// having to stand up a kube client. The production code path uses the
// same logic inline; this file exists to lock the contract down.
//
// Returned (scanTypes, gitopsProvider, namespaces, err) mirror the
// values that get passed into scanner.RunClusterScan.
func resolveScanFlags(flags *pflag.FlagSet) ([]string, string, []string, error) {
	scanTypes, _ := flags.GetStringSlice("type")
	typeFlagSet := flags.Changed("type")
	gitopsProvider, _ := flags.GetString("gitops")
	namespaceFlag, _ := flags.GetStringSlice("namespace")
	allFlag, _ := flags.GetBool("all")

	if allFlag {
		if gitopsProvider != "" && gitopsProvider != scanner.GitOpsProviderAll {
			return nil, "", nil, errMutex
		}
		scanTypes = []string{"helm"}
		gitopsProvider = scanner.GitOpsProviderAll
	}

	if gitopsProvider != "" && gitopsProvider != "none" &&
		gitopsProvider != scanner.GitOpsProviderAll && !typeFlagSet {
		scanTypes = nil
	}

	return scanTypes, gitopsProvider, namespaceFlag, nil
}

// errMutex is a sentinel for the --all/--gitops mutual-exclusion error.
// Tests just check identity, not message text.
var errMutex = assertableError("--all and --gitops are mutually exclusive")

type assertableError string

func (e assertableError) Error() string { return string(e) }

// newScanFlagSet builds a flagset shaped exactly like scanCmd's so the
// resolver function gets the same defaults and flag types in tests as
// it does in production. We register only the flags the resolver
// actually inspects.
func newScanFlagSet() *pflag.FlagSet {
	fs := pflag.NewFlagSet("scan", pflag.ContinueOnError)
	fs.StringSlice("type", []string{"helm"}, "")
	fs.Bool("all", false, "")
	fs.String("gitops", "", "")
	fs.StringSliceP("namespace", "n", nil, "")
	return fs
}

// parseAndResolve plays the args through cobra-style flag parsing then
// runs the resolver. Wraps the boilerplate so each test row is one
// line.
func parseAndResolve(t *testing.T, args ...string) ([]string, string, []string, error) {
	t.Helper()
	fs := newScanFlagSet()
	if err := fs.Parse(args); err != nil {
		t.Fatalf("flag parse failed: %v", err)
	}
	return resolveScanFlags(fs)
}

// TestResolveScanFlags is the regression net for Phase 0.b. The
// behaviours under test:
//
//   - Bare `kubuto scan` keeps the historical helm-only default.
//   - `--gitops X` (without --all and without --type) becomes
//     "GitOps-only", clearing scanTypes. This is the B1 fix.
//   - Explicit `--type` re-enables the additive shape.
//   - `--all` always means helm + GitOpsProviderAll regardless of
//     other flags (and rejects an explicit conflicting --gitops).
//   - `-n / --namespace` is a pure pass-through; it never alters
//     scanTypes / gitopsProvider.
func TestResolveScanFlags(t *testing.T) {
	type want struct {
		scanTypes []string
		gitops    string
		ns        []string
		errIs     error
	}
	cases := []struct {
		name string
		args []string
		want want
	}{
		{
			name: "bare scan: helm-only, cluster-wide, no gitops",
			args: []string{},
			want: want{scanTypes: []string{"helm"}, gitops: ""},
		},
		{
			name: "B1: --gitops argocd alone clears default --type=helm",
			args: []string{"--gitops", "argocd"},
			want: want{scanTypes: nil, gitops: "argocd"},
		},
		{
			name: "B1: --gitops flux alone clears default --type=helm",
			args: []string{"--gitops", "flux"},
			want: want{scanTypes: nil, gitops: "flux"},
		},
		{
			name: "B1 escape hatch: explicit --type with --gitops keeps both",
			args: []string{"--type", "helm", "--gitops", "argocd"},
			want: want{scanTypes: []string{"helm"}, gitops: "argocd"},
		},
		{
			name: "B1 escape hatch: --type=helm,image with --gitops flux keeps all three",
			args: []string{"--type", "helm,image", "--gitops", "flux"},
			want: want{scanTypes: []string{"helm", "image"}, gitops: "flux"},
		},
		{
			name: "--all forces helm + GitOpsProviderAll, ignoring default scanTypes",
			args: []string{"--all"},
			want: want{
				scanTypes: []string{"helm"},
				gitops:    scanner.GitOpsProviderAll,
			},
		},
		{
			name: "--all with --gitops all is allowed (idempotent)",
			args: []string{"--all", "--gitops", scanner.GitOpsProviderAll},
			want: want{
				scanTypes: []string{"helm"},
				gitops:    scanner.GitOpsProviderAll,
			},
		},
		{
			name: "--all with explicit --gitops argocd is rejected",
			args: []string{"--all", "--gitops", "argocd"},
			want: want{errIs: errMutex},
		},
		{
			name: "--gitops none is treated as 'no gitops scan' (no scanTypes clear)",
			args: []string{"--gitops", "none"},
			want: want{scanTypes: []string{"helm"}, gitops: "none"},
		},
		{
			name: "-n/--namespace passes through and does not affect scanTypes",
			args: []string{"-n", "automation,monitoring"},
			want: want{
				scanTypes: []string{"helm"},
				ns:        []string{"automation", "monitoring"},
			},
		},
		{
			name: "-n combines cleanly with --gitops argocd (still GitOps-only)",
			args: []string{"--gitops", "argocd", "-n", "devtroncd,default"},
			want: want{
				scanTypes: nil,
				gitops:    "argocd",
				ns:        []string{"devtroncd", "default"},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotTypes, gotGitops, gotNS, err := parseAndResolve(t, tc.args...)
			if tc.want.errIs != nil {
				assert.Error(t, err)
				assert.Equal(t, tc.want.errIs.Error(), err.Error())
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tc.want.scanTypes, gotTypes, "scanTypes mismatch")
			assert.Equal(t, tc.want.gitops, gotGitops, "gitopsProvider mismatch")
			// pflag.GetStringSlice returns a zero-length slice (not nil)
			// when the flag wasn't passed; ElementsMatch handles both
			// shapes correctly and is the right comparison here because
			// `-n a,b` and `-n b,a` should both succeed.
			assert.ElementsMatch(t, tc.want.ns, gotNS, "namespaces mismatch")
		})
	}
}

// TestScanCmdRegisteredFlags is a metadata-only test: it stops a
// well-meaning future refactor from quietly removing the user-facing
// flags Phase 0.b's UX contract depends on. If you delete one of these
// flag registrations, you also have to update this test (and explain
// why in the commit).
func TestScanCmdRegisteredFlags(t *testing.T) {
	required := []struct {
		name      string
		shorthand string
	}{
		{name: "type"},
		{name: "all"},
		{name: "gitops"},
		{name: "gitops-namespace"},
		{name: "namespace", shorthand: "n"},
		{name: "k8s-version"},
	}

	for _, want := range required {
		t.Run(want.name, func(t *testing.T) {
			f := scanCmd.Flag(want.name)
			if assert.NotNil(t, f, "scan flag %q must remain registered", want.name) {
				if want.shorthand != "" {
					assert.Equal(t, want.shorthand, f.Shorthand,
						"scan flag %q must keep shorthand %q", want.name, want.shorthand)
				}
			}
		})
	}
}

// _ ensures cobra is imported even if the helper above is rearranged
// in future edits; harmless side-effect of keeping the package
// well-formed.
var _ = cobra.Command{}
