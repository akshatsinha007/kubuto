package utils

import "fmt"

// ProgressTracker tracks progress of a multi-step operation.
type ProgressTracker struct {
	Current int
	Total   int
}

// NewProgressTracker creates a new progress tracker.
func NewProgressTracker(total int) *ProgressTracker {
	return &ProgressTracker{
		Current: 0,
		Total:   total,
	}
}

// Increment advances the progress counter by one.
func (p *ProgressTracker) Increment() {
	if p.Current < p.Total {
		p.Current++
	}
}

// Complete marks the tracker as fully done.
func (p *ProgressTracker) Complete() {
	p.Current = p.Total
}

// String returns a formatted progress string like "[3/10] (30%)".
func (p *ProgressTracker) String() string {
	pct := 0
	if p.Total > 0 {
		pct = (p.Current * 100) / p.Total
	}
	return fmt.Sprintf("[%d/%d] (%d%%)", p.Current, p.Total, pct)
}
