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

	compact := NoTouchDashboardReport()
	if compact.Enabled {
		t.Fatalf("compact Enabled = true, want false")
	}
	if compact.Mode != "disabled" {
		t.Fatalf("compact Mode = %q, want %q", compact.Mode, "disabled")
	}
}

func TestApplyNoTouchEnv(t *testing.T) {
	cfg := defaultNoTouchConfig()
	env := map[string]string{
		envNoTouchPollMS:             "125",
		envNoTouchTriggerDelta:       "64",
		envNoTouchTriggerConsecutive: "4",
		envNoTouchTriggerWindowMS:    "1500",
		envNoTouchCooldownMS:         "9000",
	}
	applyNoTouchEnv(&cfg, func(key string) string {
		return env[key]
	})
	cfg.normalize()

	if cfg.PollInterval != 125*time.Millisecond {
		t.Fatalf("PollInterval = %s, want %s", cfg.PollInterval, 125*time.Millisecond)
	}
	if cfg.TriggerDelta != 64 {
		t.Fatalf("TriggerDelta = %d, want 64", cfg.TriggerDelta)
	}
	if cfg.TriggerConsecutive != 4 {
		t.Fatalf("TriggerConsecutive = %d, want 4", cfg.TriggerConsecutive)
	}
	if cfg.TriggerWindow != 1500*time.Millisecond {
		t.Fatalf("TriggerWindow = %s, want %s", cfg.TriggerWindow, 1500*time.Millisecond)
	}
	if cfg.Cooldown != 9*time.Second {
		t.Fatalf("Cooldown = %s, want %s", cfg.Cooldown, 9*time.Second)
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
		return r.TriggerCount >= 1 && !r.TriggerActive && r.LastBlockProfileAt > 0 && r.LastMutexProfileAt > 0 && len(r.RecentIncidents) > 0
	})
	if r.TriggerCount < 1 {
		t.Fatalf("TriggerCount = %d, want >= 1", r.TriggerCount)
	}
	if r.LastBlockProfileAt == 0 || r.LastMutexProfileAt == 0 {
		t.Fatalf("expected captured profile timestamps, got block=%d mutex=%d", r.LastBlockProfileAt, r.LastMutexProfileAt)
	}
	incident := r.RecentIncidents[len(r.RecentIncidents)-1]
	if incident.TriggeredAt == 0 || incident.ResolvedAt == 0 {
		t.Fatalf("incident timestamps = (%d, %d), want both set", incident.TriggeredAt, incident.ResolvedAt)
	}
	if incident.PeakGoroutines == 0 {
		t.Fatal("expected incident peak goroutines to be set")
	}

	Shutdown()
	gotMutexRate := runtime.SetMutexProfileFraction(-1)
	if gotMutexRate != prevMutexRate {
		t.Fatalf("mutex profile rate = %d, want restored %d", gotMutexRate, prevMutexRate)
	}
}

func TestNoTouchIncidentRetentionAndOrdering(t *testing.T) {
	p := newNoTouchProbe(defaultNoTouchConfig())

	p.mu.Lock()
	for i := 1; i <= 7; i++ {
		p.appendIncidentLocked(NoTouchIncident{
			TriggeredAt:    int64(i),
			ResolvedAt:     int64(i) * 10,
			PeakGoroutines: 100 + i,
		})
	}
	p.mu.Unlock()

	report := p.report()
	if len(report.RecentIncidents) != defaultNoTouchIncidentLimit {
		t.Fatalf("len(RecentIncidents) = %d, want %d", len(report.RecentIncidents), defaultNoTouchIncidentLimit)
	}
	for i, incident := range report.RecentIncidents {
		want := int64(i + 3)
		if incident.TriggeredAt != want {
			t.Fatalf("RecentIncidents[%d].TriggeredAt = %d, want %d", i, incident.TriggeredAt, want)
		}
	}
}

func TestSummarizeGoroutineProfileGroupsByStateAndFrame(t *testing.T) {
	raw := `goroutine 1 [chan receive]:
runtime.gopark
	/opt/homebrew/Cellar/go/src/runtime/proc.go:424 +0x10
github.com/acme/service.(*worker).run
	/work/worker.go:10 +0x20

goroutine 2 [chan receive]:
runtime.gopark
	/opt/homebrew/Cellar/go/src/runtime/proc.go:424 +0x10
github.com/acme/service.(*worker).run
	/work/worker.go:10 +0x20

goroutine 3 [select]:
runtime.selectgo
	/opt/homebrew/Cellar/go/src/runtime/select.go:335 +0x50
github.com/acme/service.loop
	/work/loop.go:42 +0x30
`

	hotspots := summarizeGoroutineProfile(raw)
	if len(hotspots) != 2 {
		t.Fatalf("len(hotspots) = %d, want 2", len(hotspots))
	}
	if hotspots[0].State != "chan receive" || hotspots[0].TopFrame != "github.com/acme/service.(*worker).run" || hotspots[0].Count != 2 {
		t.Fatalf("first hotspot = %#v, want grouped chan receive worker.run x2", hotspots[0])
	}
	if hotspots[1].State != "select" || hotspots[1].TopFrame != "github.com/acme/service.loop" || hotspots[1].Count != 1 {
		t.Fatalf("second hotspot = %#v, want grouped select loop x1", hotspots[1])
	}
}

func TestPersistentHotspotsAcrossConsecutiveIncidents(t *testing.T) {
	current := []NoTouchHotspot{
		{State: "chan receive", TopFrame: "github.com/acme/service.(*worker).run", Count: 4},
		{State: "select", TopFrame: "github.com/acme/service.loop", Count: 1},
	}
	previous := []NoTouchHotspot{
		{State: "chan receive", TopFrame: "github.com/acme/service.(*worker).run", Count: 3},
		{State: "select", TopFrame: "github.com/acme/service.loop", Count: 2},
	}

	markPersistentHotspots(current, previous)

	if !current[0].Persistent {
		t.Fatal("expected first hotspot to be marked persistent")
	}
	if current[1].Persistent {
		t.Fatal("expected second hotspot to remain non-persistent because count decreased")
	}
}
