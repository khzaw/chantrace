package chantrace

import (
	"reflect"
	"testing"
	"time"
)

// These tests cover edge cases for the no-touch incident-reporting machinery
// that landed alongside the wrapper-API removal: env-var parsing robustness,
// goroutine-profile summarization corner cases, and the compact report path.
// The happy paths are covered in notouch_test.go.

// --- env-var parsing ---

func TestApplyNoTouchEnvIgnoresGarbageAndEmpty(t *testing.T) {
	cfg := defaultNoTouchConfig()
	want := cfg // snapshot before env application

	env := map[string]string{
		envNoTouchPollMS:             "not-a-number",
		envNoTouchTriggerDelta:       "",
		envNoTouchTriggerConsecutive: "   ",
		envNoTouchTriggerWindowMS:    "0x10",
		envNoTouchCooldownMS:         "-5", // parses, then normalize leaves negatives? Cooldown<0 → 0
	}
	applyNoTouchEnv(&cfg, func(key string) string { return env[key] })

	// Garbage and empty values must leave the field untouched.
	if cfg.PollInterval != want.PollInterval {
		t.Errorf("PollInterval = %v, want unchanged %v", cfg.PollInterval, want.PollInterval)
	}
	if cfg.TriggerDelta != want.TriggerDelta {
		t.Errorf("TriggerDelta = %d, want unchanged %d", cfg.TriggerDelta, want.TriggerDelta)
	}
	if cfg.TriggerConsecutive != want.TriggerConsecutive {
		t.Errorf("TriggerConsecutive = %d, want unchanged %d", cfg.TriggerConsecutive, want.TriggerConsecutive)
	}
	if cfg.TriggerWindow != want.TriggerWindow {
		t.Errorf("TriggerWindow = %v, want unchanged %v", cfg.TriggerWindow, want.TriggerWindow)
	}
	// Cooldown parses -5 → -5ms, which normalize() later clamps to 0. Here we
	// only assert the parse happened (not the clamp), so it should be -5ms.
	if cfg.Cooldown != -5*time.Millisecond {
		t.Errorf("Cooldown = %v, want -5ms (parsed; clamp happens in normalize)", cfg.Cooldown)
	}
}

func TestApplyNoTouchEnvTrimsWhitespace(t *testing.T) {
	cfg := defaultNoTouchConfig()
	env := map[string]string{
		envNoTouchTriggerDelta: "  42  ",
	}
	applyNoTouchEnv(&cfg, func(key string) string { return env[key] })
	if cfg.TriggerDelta != 42 {
		t.Errorf("TriggerDelta = %d, want 42 (whitespace trimmed)", cfg.TriggerDelta)
	}
}

func TestApplyNoTouchEnvMissingKeysPreserveDefaults(t *testing.T) {
	cfg := defaultNoTouchConfig()
	want := cfg
	// getenv returns "" for every key (simulating an unset environment).
	applyNoTouchEnv(&cfg, func(string) string { return "" })
	if !reflect.DeepEqual(cfg, want) {
		t.Errorf("cfg changed when no env vars set: got %+v, want %+v", cfg, want)
	}
}

func TestEnvInt(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want int
		ok   bool
	}{
		{"empty", "", 0, false},
		{"whitespace", "  ", 0, false},
		{"garbage", "abc", 0, false},
		{"valid", "123", 123, true},
		{"negative", "-7", -7, true},
		{"float-rejected", "1.5", 0, false},
	}
	getenv := func(raw string) func(string) string {
		return func(string) string { return raw }
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := envInt(getenv(tc.raw), "X")
			if ok != tc.ok {
				t.Errorf("ok = %v, want %v", ok, tc.ok)
			}
			if got != tc.want {
				t.Errorf("got = %d, want %d", got, tc.want)
			}
		})
	}
}

// --- goroutineState ---

func TestGoroutineState(t *testing.T) {
	cases := []struct {
		header string
		want   string
	}{
		{"goroutine 1 [chan receive]:", "chan receive"},
		{"goroutine 1 [chan receive, 5 minutes]:", "chan receive"}, // comma-trimmed
		{"goroutine 1 [select]:", "select"},
		{"goroutine 1 [runnable]:", "runnable"},
		{"goroutine 1 no brackets", "unknown"},
		{"goroutine 1 [unterminated", "unknown"},
		{"goroutine 1 []:", "unknown"}, // empty state
		{"", "unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.header, func(t *testing.T) {
			if got := goroutineState(tc.header); got != tc.want {
				t.Errorf("goroutineState(%q) = %q, want %q", tc.header, got, tc.want)
			}
		})
	}
}

// --- topNonRuntimeFrame ---

func TestTopNonRuntimeFrame(t *testing.T) {
	// The first non-indented, non-runtime, non-"created by" frame wins.
	lines := []string{
		"runtime.gopark",
		"\t/opt/go/src/runtime/proc.go:424 +0x10",
		"github.com/acme/service.(*worker).run",
		"\t/work/worker.go:10 +0x20",
		"created by github.com/acme/service.main in goroutine 1",
		"\t/work/main.go:15 +0x40",
	}
	if got := topNonRuntimeFrame(lines); got != "github.com/acme/service.(*worker).run" {
		t.Errorf("topNonRuntimeFrame = %q, want first user frame", got)
	}
}

func TestTopNonRuntimeFrameAllRuntimeFallsBackToFirst(t *testing.T) {
	// No user frames present → fall back to the first frame seen.
	lines := []string{
		"runtime.gopark",
		"\t/opt/go/src/runtime/proc.go:424 +0x10",
	}
	if got := topNonRuntimeFrame(lines); got != "runtime.gopark" {
		t.Errorf("topNonRuntimeFrame = %q, want fallback to first frame runtime.gopark", got)
	}
}

func TestTopNonRuntimeFrameEmpty(t *testing.T) {
	if got := topNonRuntimeFrame(nil); got != "unknown" {
		t.Errorf("topNonRuntimeFrame(nil) = %q, want unknown", got)
	}
	if got := topNonRuntimeFrame([]string{}); got != "unknown" {
		t.Errorf("topNonRuntimeFrame([]) = %q, want unknown", got)
	}
}

// --- summarizeGoroutineProfile ---

func TestSummarizeGoroutineProfileEmpty(t *testing.T) {
	if got := summarizeGoroutineProfile(""); got != nil {
		t.Errorf("summarizeGoroutineProfile(\"\") = %#v, want nil", got)
	}
	if got := summarizeGoroutineProfile("   \n  "); got != nil {
		t.Errorf("summarizeGoroutineProfile(whitespace) = %#v, want nil", got)
	}
}

func TestSummarizeGoroutineProfileSkipsNonGoroutineBlocks(t *testing.T) {
	// A block that doesn't start with "goroutine " must be skipped.
	raw := "some preamble line\nmore preamble\n\ngoroutine 1 [select]:\nruntime.selectgo\n\t/runtime/select.go:1\ngithub.com/acme.x\n\t/x.go:1"
	hotspots := summarizeGoroutineProfile(raw)
	if len(hotspots) != 1 {
		t.Fatalf("len(hotspots) = %d, want 1 (preamble skipped)", len(hotspots))
	}
	if hotspots[0].Count != 1 {
		t.Errorf("hotspot count = %d, want 1", hotspots[0].Count)
	}
}

func TestSummarizeGoroutineProfileSortedByCountDescThenStateThenFrame(t *testing.T) {
	raw := `goroutine 1 [select]:
runtime.selectgo
	/runtime/select.go:1
github.com/acme.b
	/b.go:1

goroutine 2 [chan receive]:
runtime.gopark
	/runtime/proc.go:1
github.com/acme.a
	/a.go:1

goroutine 3 [chan receive]:
runtime.gopark
	/runtime/proc.go:1
github.com/acme.a
	/a.go:1
`
	hotspots := summarizeGoroutineProfile(raw)
	if len(hotspots) != 2 {
		t.Fatalf("len(hotspots) = %d, want 2", len(hotspots))
	}
	// Count 2 (chan receive / a) sorts before count 1 (select / b).
	if hotspots[0].Count != 2 || hotspots[0].State != "chan receive" {
		t.Errorf("hotspots[0] = %+v, want count=2 chan receive first", hotspots[0])
	}
}

// --- markPersistentHotspots edge cases ---

func TestMarkPersistentHotspotsEmptyPreviousNoOp(t *testing.T) {
	current := []NoTouchHotspot{{State: "x", TopFrame: "y", Count: 5}}
	markPersistentHotspots(current, nil)
	if current[0].Persistent {
		t.Error("hotspot marked persistent with no previous incidents")
	}
}

func TestMarkPersistentHotspotsEmptyCurrentNoOp(t *testing.T) {
	// Empty current must not panic and must be a no-op.
	markPersistentHotspots(nil, []NoTouchHotspot{{State: "x", TopFrame: "y", Count: 5}})
}

// --- compactReport via probe ---

func TestCompactReportShape(t *testing.T) {
	p := newNoTouchProbe(defaultNoTouchConfig())
	p.mu.Lock()
	p.baselineReady = true
	p.baseline = 10
	p.currentGoroutines = 15
	p.currentDelta = 5
	p.triggerCount = 2
	p.mu.Unlock()

	rep := p.compactReport()
	if !rep.Enabled || rep.Mode != "passive" {
		t.Errorf("Enabled=%v Mode=%q, want true/passive", rep.Enabled, rep.Mode)
	}
	if rep.Baseline != 10 || rep.CurrentGoroutines != 15 || rep.CurrentDelta != 5 {
		t.Errorf("counts = baseline=%d cur=%d delta=%d, want 10/15/5", rep.Baseline, rep.CurrentGoroutines, rep.CurrentDelta)
	}
	if rep.TriggerCount != 2 {
		t.Errorf("TriggerCount = %d, want 2", rep.TriggerCount)
	}
}

// --- incident ring buffer empty guard ---

func TestAppendIncidentEmptySliceNoOp(t *testing.T) {
	p := newNoTouchProbe(defaultNoTouchConfig())
	p.incidents = nil // force the empty-slice guard path
	p.mu.Lock()
	p.appendIncidentLocked(NoTouchIncident{TriggeredAt: 1})
	p.mu.Unlock()
	if p.incidentCount != 0 {
		t.Errorf("incidentCount = %d, want 0 when incidents slice is empty", p.incidentCount)
	}
}

// --- profileSummary for a missing profile ---

func TestProfileSummaryMissingProfileReturnsEmpty(t *testing.T) {
	got := profileSummary("definitely-not-a-real-profile-name", 1024, 10)
	if got != "" {
		t.Errorf("profileSummary for missing profile = %q, want empty", got)
	}
}
