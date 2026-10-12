package main

import (
	"strings"
	"testing"
	"time"

	"homelab/agents/internal/session"
)

func TestDescribeWakeup(t *testing.T) {
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	got := describeWakeup(&session.WakeupStatus{
		Pending: &session.Wakeup{At: now.Add(90 * time.Minute), Note: "end-of-day check-in"},
	}, now)
	if !strings.Contains(got, "(in 1h30m0s). Note: end-of-day check-in") {
		t.Errorf("pending: %q", got)
	}

	got = describeWakeup(&session.WakeupStatus{NextFloorSeconds: 86400, Rejected: "at is too soon"}, now)
	if !strings.Contains(got, "a wake-up turn is running") || !strings.Contains(got, "24h0m0s after it") ||
		!strings.Contains(got, "[wake-up request refused] at is too soon") {
		t.Errorf("running: %q", got)
	}
}
