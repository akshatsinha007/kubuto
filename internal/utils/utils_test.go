package utils

import (
	"fmt"
	"testing"
)

func TestNewProgressTracker(t *testing.T) {
	p := NewProgressTracker(10)
	if p.Current != 0 {
		t.Errorf("expected Current=0, got %d", p.Current)
	}
	if p.Total != 10 {
		t.Errorf("expected Total=10, got %d", p.Total)
	}
}

func TestProgressTracker_Increment(t *testing.T) {
	p := NewProgressTracker(5)
	p.Increment()
	if p.Current != 1 {
		t.Errorf("expected Current=1, got %d", p.Current)
	}
	p.Increment()
	p.Increment()
	if p.Current != 3 {
		t.Errorf("expected Current=3, got %d", p.Current)
	}
}

func TestProgressTracker_Increment_DoesNotExceedTotal(t *testing.T) {
	p := NewProgressTracker(2)
	p.Increment()
	p.Increment()
	p.Increment() // Should not go beyond Total
	if p.Current != 2 {
		t.Errorf("expected Current=2 (capped), got %d", p.Current)
	}
}

func TestProgressTracker_Complete(t *testing.T) {
	p := NewProgressTracker(10)
	p.Increment()
	p.Increment()
	p.Complete()
	if p.Current != 10 {
		t.Errorf("expected Current=10 after Complete(), got %d", p.Current)
	}
}

func TestProgressTracker_String(t *testing.T) {
	tests := []struct {
		current  int
		total    int
		expected string
	}{
		{0, 10, "[0/10] (0%)"},
		{3, 10, "[3/10] (30%)"},
		{10, 10, "[10/10] (100%)"},
		{0, 0, "[0/0] (0%)"},
	}

	for _, tt := range tests {
		p := &ProgressTracker{Current: tt.current, Total: tt.total}
		got := p.String()
		if got != tt.expected {
			t.Errorf("ProgressTracker{%d, %d}.String() = %q, want %q", tt.current, tt.total, got, tt.expected)
		}
	}
}

func TestScanError_WithNamespace(t *testing.T) {
	e := ScanError{
		Resource:  "my-release",
		Namespace: "default",
		Err:       fmt.Errorf("connection refused"),
	}
	expected := "default/my-release: connection refused"
	if e.Error() != expected {
		t.Errorf("got %q, want %q", e.Error(), expected)
	}
}

func TestScanError_WithoutNamespace(t *testing.T) {
	e := ScanError{
		Resource: "my-release",
		Err:      fmt.Errorf("not found"),
	}
	expected := "my-release: not found"
	if e.Error() != expected {
		t.Errorf("got %q, want %q", e.Error(), expected)
	}
}

func TestFormatErrors_Empty(t *testing.T) {
	got := FormatErrors([]ScanError{})
	if got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestFormatErrors_Single(t *testing.T) {
	errs := []ScanError{
		{Resource: "app", Namespace: "ns", Err: fmt.Errorf("failed")},
	}
	got := FormatErrors(errs)
	if got == "" {
		t.Error("expected non-empty output")
	}
	if !containsStr(got, "Encountered 1 error") {
		t.Errorf("expected header with count, got: %s", got)
	}
	if !containsStr(got, "ns/app") {
		t.Errorf("expected resource path, got: %s", got)
	}
}

func TestFormatErrors_Multiple(t *testing.T) {
	errs := []ScanError{
		{Resource: "a", Namespace: "ns1", Err: fmt.Errorf("err1")},
		{Resource: "b", Namespace: "ns2", Err: fmt.Errorf("err2")},
		{Resource: "c", Err: fmt.Errorf("err3")},
	}
	got := FormatErrors(errs)
	if !containsStr(got, "Encountered 3 error") {
		t.Errorf("expected count of 3, got: %s", got)
	}
	if !containsStr(got, "ns1/a") {
		t.Errorf("expected ns1/a, got: %s", got)
	}
	if !containsStr(got, "c: err3") {
		t.Errorf("expected c: err3, got: %s", got)
	}
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
