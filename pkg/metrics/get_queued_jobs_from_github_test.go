package metrics

import (
	"testing"
	"time"

	"github.com/google/go-github/v45/github"
)

func TestCountQueuedJobs(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	str := func(s string) *string { return &s }
	ts := func(ago time.Duration) *github.Timestamp { return &github.Timestamp{Time: now.Add(-ago)} }

	jobs := []*github.WorkflowJob{
		{Status: str("queued"), Labels: []string{"hetzner-large"}, StartedAt: ts(90 * time.Second)},
		{Status: str("queued"), Labels: []string{"hetzner-large"}, StartedAt: ts(30 * time.Second)},
		{Status: str("in_progress"), Labels: []string{"ubuntu-latest"}, StartedAt: ts(5 * time.Minute)},
		{Status: str("completed"), Labels: []string{"hetzner-large"}},
		{Status: str("queued"), Labels: nil},
	}

	count := make(map[queuedKey]float64)
	oldest := make(map[queuedKey]float64)
	countQueuedJobs("juroteam/juro-nx", jobs, now, count, oldest)

	large := queuedKey{repo: "juroteam/juro-nx", labels: "hetzner-large"}
	ubuntu := queuedKey{repo: "juroteam/juro-nx", labels: "ubuntu-latest"}

	if len(count) != 2 {
		t.Fatalf("expected 2 label keys, got %d: %v", len(count), count)
	}
	if count[large] != 2 {
		t.Errorf("hetzner-large queued = %v, want 2", count[large])
	}
	if oldest[large] != 90 {
		t.Errorf("hetzner-large oldest = %v, want 90", oldest[large])
	}
	if count[ubuntu] != 0 || oldest[ubuntu] != 0 {
		t.Errorf("ubuntu-latest = %v/%v, want 0/0", count[ubuntu], oldest[ubuntu])
	}
}
