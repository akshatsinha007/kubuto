package output

import (
	"testing"

	"github.com/akshatsinha007/kubuto/pkg/types"
)

func TestTransparencyNotes_OCIOnly(t *testing.T) {
	resources := []types.Resource{
		{Name: "a", Repository: "oci://registry.example.com/charts", LatestVersion: ""},
	}
	notes := TransparencyNotes(resources)
	if len(notes) != 1 {
		t.Fatalf("expected 1 note, got %d: %v", len(notes), notes)
	}
	if got, want := notes[0], "1 chart(s) sourced from OCI registries — latest-version lookup isn't supported for OCI repos yet."; got != want {
		t.Errorf("note = %q, want %q", got, want)
	}
}

func TestTransparencyNotes_GitPinOnly(t *testing.T) {
	resources := []types.Resource{
		{Name: "a", CurrentVersion: "git@abc1234"},
	}
	notes := TransparencyNotes(resources)
	if len(notes) != 1 {
		t.Fatalf("expected 1 note, got %d: %v", len(notes), notes)
	}
	if got, want := notes[0], "1 chart(s) pinned to a git revision, not a real chart version (non-Helm or unresolvable source)."; got != want {
		t.Errorf("note = %q, want %q", got, want)
	}
}

func TestTransparencyNotes_Neither(t *testing.T) {
	resources := []types.Resource{
		{Name: "a", Repository: "https://charts.example.com", CurrentVersion: "1.2.3", LatestVersion: "1.3.0"},
		{Name: "b", Repository: "oci://registry.example.com/charts", CurrentVersion: "1.0.0", LatestVersion: "1.1.0"}, // OCI but LatestVersion is set — not a no-op case
	}
	notes := TransparencyNotes(resources)
	if len(notes) != 0 {
		t.Fatalf("expected no notes, got %v", notes)
	}
}

func TestTransparencyNotes_Mixed(t *testing.T) {
	resources := []types.Resource{
		{Name: "a", Repository: "oci://registry.example.com/charts", LatestVersion: ""},
		{Name: "b", Repository: "oci://registry.example.com/other", LatestVersion: ""},
		{Name: "c", CurrentVersion: "git@abc1234"},
	}
	notes := TransparencyNotes(resources)
	if len(notes) != 2 {
		t.Fatalf("expected 2 notes, got %d: %v", len(notes), notes)
	}
	if got, want := notes[0], "2 chart(s) sourced from OCI registries — latest-version lookup isn't supported for OCI repos yet."; got != want {
		t.Errorf("notes[0] = %q, want %q", got, want)
	}
	if got, want := notes[1], "1 chart(s) pinned to a git revision, not a real chart version (non-Helm or unresolvable source)."; got != want {
		t.Errorf("notes[1] = %q, want %q", got, want)
	}
}
