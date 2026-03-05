package chantrace

import (
	"runtime"
	"testing"
	"time"
)

func waitForNoTouch(t *testing.T, timeout time.Duration, pred func(NoTouchSnapshot) bool) NoTouchSnapshot {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		r := NoTouchReport()
		if pred(r) {
			return r
		}
		time.Sleep(5 * time.Millisecond)
	}
	return NoTouchReport()
}

func TestNoTouchReportDisabledByDefault(t *testing.T) {
	Shutdown()
	r := NoTouchReport()
	if r.Enabled {
		t.Fatalf("Enabled = true, want false")
	}
	if r.Mode != "disabled" {
		t.Fatalf("Mode = %q, want %q", r.Mode, "disabled")
	}
}

func TestNoTouchLifecycle(t *testing.T) {
	Shutdown()
	Enable(
		WithNoTouch(
			WithNoTouchPollInterval(10*time.Millisecond),
			WithNoTouchBaselineSamples(1),
			WithNoTouchTriggerDelta(1<<30), // effectively never trigger
			WithNoTouchHistorySize(32),
		),
	)
	defer Shutdown()

	r := waitForNoTouch(t, time.Second, func(r NoTouchSnapshot) bool {
		return r.Enabled && len(r.Samples) >= 3
	})
	if !r.Enabled {
		t.Fatal("expected no-touch to be enabled")
	}
	if len(r.Samples) == 0 {
		t.Fatal("expected passive samples in report")
	}
	if r.Mode == "disabled" {
		t.Fatalf("Mode = %q, expected active mode", r.Mode)
	}

	Enable(WithLogStream())
	r = waitForNoTouch(t, time.Second, func(r NoTouchSnapshot) bool {
		return !r.Enabled
	})
	if r.Enabled {
		t.Fatal("expected no-touch to be disabled after re-enable without WithNoTouch")
	}
}

func TestNoTouchTriggerWindowCapturesAndRestoresMutexRate(t *testing.T) {
	Shutdown()
	prevMutexRate := runtime.SetMutexProfileFraction(-1)

	Enable(
		WithNoTouch(
			WithNoTouchPollInterval(8*time.Millisecond),
			WithNoTouchBaselineSamples(1),
			WithNoTouchTriggerDelta(0),
			WithNoTouchTriggerConsecutive(1),
			WithNoTouchTriggerWindow(30*time.Millisecond),
			WithNoTouchCooldown(20*time.Millisecond),
			WithNoTouchBlockProfileRate(1),
			WithNoTouchMutexProfileFraction(1),
			WithNoTouchProfileSummaryLines(20),
			WithNoTouchProfileMaxBytes(4<<10),
		),
	)
	defer Shutdown()

	r := waitForNoTouch(t, 2*time.Second, func(r NoTouchSnapshot) bool {
		return r.TriggerCount >= 1 && !r.TriggerActive && r.LastBlockProfileAt > 0 && r.LastMutexProfileAt > 0
	})
	if r.TriggerCount < 1 {
		t.Fatalf("TriggerCount = %d, want >= 1", r.TriggerCount)
	}
	if r.LastBlockProfileAt == 0 || r.LastMutexProfileAt == 0 {
		t.Fatalf("expected captured profile timestamps, got block=%d mutex=%d", r.LastBlockProfileAt, r.LastMutexProfileAt)
	}

	Shutdown()
	gotMutexRate := runtime.SetMutexProfileFraction(-1)
	if gotMutexRate != prevMutexRate {
		t.Fatalf("mutex profile rate = %d, want restored %d", gotMutexRate, prevMutexRate)
	}
}
