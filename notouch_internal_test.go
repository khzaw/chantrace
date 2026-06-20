package chantrace

import (
	"runtime"
	"strings"
	"testing"
	"time"
)

// These tests exercise the no-touch internals directly. They target the
// behavior the public-API tests can't pin down deterministically: config
// normalization, the trigger state machine, ring-buffer wrap, block-rate
// restoration, profile summary content, and cooldown suppression. Now that
// no-touch is the sole flagship, this is the critical correctness surface.

// TestNormalizeClamping verifies that normalize() replaces zero/negative
// config values with the documented defaults and clamps others.
func TestNormalizeClamping(t *testing.T) {
	c := NoTouchConfig{} // all zero values
	c.normalize()

	checks := []struct {
		name string
		got  int
		want int
	}{
		{"PollInterval(ms)", int(c.PollInterval / time.Millisecond), int(defaultNoTouchPollInterval / time.Millisecond)},
		{"HistorySize", c.HistorySize, defaultNoTouchHistorySize},
		{"BaselineSamples", c.BaselineSamples, 1},
		{"TriggerDelta", c.TriggerDelta, 0},
		{"TriggerConsecutive", c.TriggerConsecutive, 1},
		{"TriggerWindow(ms)", int(c.TriggerWindow / time.Millisecond), int(defaultNoTouchTriggerWindow / time.Millisecond)},
		{"Cooldown(ms)", int(c.Cooldown / time.Millisecond), 0},
		{"BlockProfileRate", c.BlockProfileRate, defaultNoTouchBlockProfileRate},
		{"BlockProfileRestore", c.BlockProfileRestore, 0},
		{"MutexProfileFraction", c.MutexProfileFraction, 0}, // <0 → 0, not the default rate
		{"ProfileMaxBytes", c.ProfileMaxBytes, defaultNoTouchProfileMaxBytes},
		{"ProfileSummaryLines", c.ProfileSummaryLines, defaultNoTouchProfileSummaryLines},
	}
	for _, ck := range checks {
		if ck.got != ck.want {
			t.Errorf("%s = %d, want %d", ck.name, ck.got, ck.want)
		}
	}
}

// TestNormalizeClampingNegativeMutexFraction checks the <0 clamp path leaves
// MutexProfileFraction at 0 even when a default rate constant exists.
func TestNormalizeClampingNegativeMutexFraction(t *testing.T) {
	c := NoTouchConfig{MutexProfileFraction: -5}
	c.normalize()
	if c.MutexProfileFraction != 0 {
		t.Errorf("MutexProfileFraction = %d, want 0 (clamped from negative)", c.MutexProfileFraction)
	}
}

// TestNormalizePreservesExplicitValues verifies that sane non-zero values are
// left alone by normalize.
func TestNormalizePreservesExplicitValues(t *testing.T) {
	c := NoTouchConfig{
		PollInterval:         42 * time.Millisecond,
		HistorySize:          7,
		BaselineSamples:      3,
		TriggerDelta:         10,
		TriggerConsecutive:   2,
		TriggerWindow:        99 * time.Millisecond,
		Cooldown:             50 * time.Millisecond,
		BlockProfileRate:     1,
		BlockProfileRestore:  0,
		MutexProfileFraction: 4,
		ProfileMaxBytes:      2048,
		ProfileSummaryLines:  5,
	}
	c.normalize()
	if c.PollInterval != 42*time.Millisecond {
		t.Errorf("PollInterval changed to %v", c.PollInterval)
	}
	if c.TriggerConsecutive != 2 {
		t.Errorf("TriggerConsecutive changed to %d", c.TriggerConsecutive)
	}
	if c.MutexProfileFraction != 4 {
		t.Errorf("MutexProfileFraction changed to %d", c.MutexProfileFraction)
	}
}

// driveProbe builds a probe with a fast schedule suitable for the state-machine
// tests, with trigger machinery configured but the run loop NOT started. Its
// numGoroutines hook is seeded to a fixed value and moved forward per-tick via
// tickAt, so the delta/consecutive logic is fully deterministic.
func driveProbe() *noTouchProbe {
	cfg := defaultNoTouchConfig()
	cfg.PollInterval = time.Millisecond
	cfg.BaselineSamples = 1
	cfg.TriggerDelta = 5
	cfg.TriggerConsecutive = 2
	cfg.TriggerWindow = 10 * time.Millisecond
	cfg.Cooldown = 20 * time.Millisecond
	cfg.BlockProfileRate = 1
	cfg.MutexProfileFraction = 1
	p := newNoTouchProbe(cfg)
	return p
}

// tickAt drives a single tick reporting goroutine count g at time now.
func tickAt(p *noTouchProbe, g int, now time.Time) {
	p.numGoroutines = func() int { return g }
	p.tick(now)
}

// TestTriggerConsecutiveAccumulationAndDipReset verifies that:
//   - an anomalous sample below TriggerConsecutive does NOT fire;
//   - a non-anomalous sample between anomalies resets the counter;
//   - reaching TriggerConsecutive fires exactly one trigger.
func TestTriggerConsecutiveAccumulationAndDipReset(t *testing.T) {
	p := driveProbe()
	t0 := time.Now()

	// Tick 1: establishes baseline (BaselineSamples=1). g=10 → baseline=10.
	tickAt(p, 10, t0)
	if !p.baselineReady {
		t.Fatal("baseline should be ready after BaselineSamples ticks")
	}
	if p.baseline != 10 {
		t.Fatalf("baseline = %d, want 10", p.baseline)
	}

	// Tick 2: delta=+20 (>=5) → consecutiveAnomaly=1, below threshold of 2.
	tickAt(p, 30, t0.Add(time.Millisecond))
	if p.triggerActive || p.triggerCount != 0 {
		t.Fatalf("after one anomalous sample: triggerActive=%v count=%d, want no trigger",
			p.triggerActive, p.triggerCount)
	}

	// Tick 3: delta back to 0 → counter resets to 0.
	tickAt(p, 10, t0.Add(2*time.Millisecond))
	if p.consecutiveAnomaly != 0 {
		t.Fatalf("consecutiveAnomaly = %d, want 0 after dip", p.consecutiveAnomaly)
	}

	// Ticks 4-5: two anomalous samples in a row → trigger fires on tick 5.
	tickAt(p, 30, t0.Add(3*time.Millisecond))
	tickAt(p, 30, t0.Add(4*time.Millisecond))
	if p.triggerCount != 1 {
		t.Fatalf("triggerCount = %d, want 1 after two consecutive anomalies", p.triggerCount)
	}
	if !p.triggerActive {
		t.Fatal("triggerActive = false, want true after firing")
	}
	// Firing resets the anomaly counter.
	if p.consecutiveAnomaly != 0 {
		t.Fatalf("consecutiveAnomaly = %d after trigger, want 0", p.consecutiveAnomaly)
	}
}

// TestCooldownSuppressesRetrigger verifies that after a trigger window closes,
// a fresh anomaly during cooldown does not immediately re-open a window.
func TestCooldownSuppressesRetrigger(t *testing.T) {
	p := driveProbe() // Cooldown=20ms, TriggerWindow=10ms
	t0 := time.Now()

	tickAt(p, 10, t0) // baseline=10

	// Two anomalies → trigger opens at t0+2ms, window closes at t0+12ms.
	tickAt(p, 50, t0.Add(1*time.Millisecond))
	tickAt(p, 50, t0.Add(2*time.Millisecond))
	if p.triggerCount != 1 {
		t.Fatalf("triggerCount = %d, want 1", p.triggerCount)
	}

	// Advance past the window end → window closes, cooldown starts (until +20ms → t0+33ms).
	tickAt(p, 50, t0.Add(13*time.Millisecond))
	if p.triggerActive {
		t.Fatal("trigger should be inactive after window elapsed")
	}
	countAfterWindow := p.triggerCount

	// During cooldown (delta still anomalous) → must NOT retrigger.
	tickAt(p, 50, t0.Add(15*time.Millisecond))
	tickAt(p, 50, t0.Add(17*time.Millisecond))
	if p.triggerCount != countAfterWindow {
		t.Fatalf("retriggered during cooldown: count=%d, want %d", p.triggerCount, countAfterWindow)
	}

	// Past cooldown + TriggerConsecutive anomalies → retriggers.
	tickAt(p, 50, t0.Add(34*time.Millisecond)) // cooldown ended at +33ms
	tickAt(p, 50, t0.Add(35*time.Millisecond))
	if p.triggerCount <= countAfterWindow {
		t.Fatalf("did not retrigger after cooldown: count=%d, want > %d", p.triggerCount, countAfterWindow)
	}
}

// TestModeTransitions checks the Mode string across warmup → passive →
// triggered → passive.
func TestModeTransitions(t *testing.T) {
	p := driveProbe()
	p.cfg.BaselineSamples = 3 // need a warmup phase we can observe
	t0 := time.Now()

	// Warmup: not enough baseline samples yet.
	tickAt(p, 10, t0)
	tickAt(p, 10, t0.Add(time.Millisecond))
	if mode := modeOf(p); mode != "warming-up" {
		t.Fatalf("mode = %q during warmup, want %q", mode, "warming-up")
	}

	// Baseline locks in on the 3rd sample → passive.
	tickAt(p, 10, t0.Add(2*time.Millisecond))
	if mode := modeOf(p); mode != "passive" {
		t.Fatalf("mode = %q after baseline, want %q", mode, "passive")
	}

	// Two anomalies → triggered.
	tickAt(p, 60, t0.Add(3*time.Millisecond))
	tickAt(p, 60, t0.Add(4*time.Millisecond))
	if mode := modeOf(p); mode != "triggered" {
		t.Fatalf("mode = %q during window, want %q", mode, "triggered")
	}

	// Window elapses → passive again.
	tickAt(p, 60, t0.Add(20*time.Millisecond))
	if mode := modeOf(p); mode != "passive" {
		t.Fatalf("mode = %q after window, want %q", mode, "passive")
	}
}

func modeOf(p *noTouchProbe) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	mode := "passive"
	if !p.baselineReady {
		mode = "warming-up"
	}
	if p.triggerActive {
		mode = "triggered"
	}
	return mode
}

// TestRingBufferWrap drives more ticks than HistorySize and confirms the ring
// wraps correctly: sampleCount stays capped, head advances, and the report
// returns samples in chronological order with the oldest overwritten.
func TestRingBufferWrap(t *testing.T) {
	p := driveProbe()
	p.cfg.HistorySize = 4
	p.cfg.BaselineSamples = 1
	p.cfg.TriggerDelta = 1 << 30 // never trigger
	p.samples = make([]NoTouchSample, p.cfg.HistorySize) // re-allocate with new size
	t0 := time.Now()

	// Fill 6 ticks into a ring of 4. sampleCount caps at 4.
	for i := 0; i < 6; i++ {
		tickAt(p, 10+i, t0.Add(time.Duration(i)*time.Millisecond))
	}
	if p.sampleCount != 4 {
		t.Fatalf("sampleCount = %d, want 4 (capped at HistorySize)", p.sampleCount)
	}
	if p.sampleHead != 2 { // 6 % 4
		t.Fatalf("sampleHead = %d, want 2", p.sampleHead)
	}

	rep := p.report()
	if len(rep.Samples) != 4 {
		t.Fatalf("report has %d samples, want 4", len(rep.Samples))
	}
	// Oldest two (goroutines 10,11) overwritten; retained series starts at g=12.
	want := []int{12, 13, 14, 15}
	for i, s := range rep.Samples {
		if s.Goroutines != want[i] {
			t.Errorf("Samples[%d].Goroutines = %d, want %d", i, s.Goroutines, want[i])
		}
	}
}

// TestBlockProfileRateRestored verifies that when the trigger window closes,
// the probe records the captured block profile and stops the active window.
// (The modern runtime API exposes no reader for SetBlockProfileRate's current
// value, so we assert the observable effects: the window closes and a block
// profile timestamp is captured. The restore call itself runs in
// stopTriggerLocked via SetBlockProfileRate(cfg.BlockProfileRestore).)
func TestBlockProfileRateRestored(t *testing.T) {
	runtime.SetBlockProfileRate(0)
	t.Cleanup(func() { runtime.SetBlockProfileRate(0) })

	p := driveProbe()
	p.cfg.BlockProfileRestore = 0
	p.cfg.MutexProfileFraction = 0
	p.cfg.TriggerConsecutive = 1
	t0 := time.Now()

	tickAt(p, 10, t0) // baseline
	tickAt(p, 60, t0.Add(time.Millisecond))
	if p.triggerCount != 1 {
		t.Fatalf("triggerCount = %d, want 1", p.triggerCount)
	}

	// Elapse the window → stopTriggerLocked runs, capturing profiles.
	tickAt(p, 60, t0.Add(15*time.Millisecond))
	if p.triggerActive {
		t.Error("triggerActive = true after window elapsed, want false")
	}
	if p.lastBlockProfileAt == 0 {
		t.Error("lastBlockProfileAt = 0, want captured timestamp after window close")
	}
}

// TestProfileSummaryTruncatesLines verifies that profileSummary respects the
// summaryLines cap and returns at most that many lines.
func TestProfileSummaryTruncatesLines(t *testing.T) {
	// The "goroutine" profile always exists while a test is running; use it as
	// a guaranteed-non-empty stand-in for block/mutex (which may be empty if no
	// contention has been sampled).
	s := profileSummary("goroutine", defaultNoTouchProfileMaxBytes, 3)
	if s == "" {
		t.Skip("goroutine profile empty; cannot verify truncation")
	}
	lines := strings.Count(s, "\n") + 1
	if lines > 3 {
		t.Errorf("profileSummary returned %d lines, want <= 3", lines)
	}
}

