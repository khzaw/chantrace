package debug

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/khzaw/chantrace"
)

func waitForNoTouchReport(t *testing.T, timeout time.Duration, pred func(chantrace.NoTouchSnapshot) bool) chantrace.NoTouchSnapshot {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		report := chantrace.NoTouchReport()
		if pred(report) {
			return report
		}
		time.Sleep(5 * time.Millisecond)
	}
	return chantrace.NoTouchReport()
}

func TestHandleNoTouchReturnsStructuredFields(t *testing.T) {
	chantrace.Shutdown()
	t.Cleanup(chantrace.Shutdown)

	chantrace.Enable(
		chantrace.WithNoTouch(
			chantrace.WithNoTouchPollInterval(8*time.Millisecond),
			chantrace.WithNoTouchBaselineSamples(1),
			chantrace.WithNoTouchTriggerDelta(0),
			chantrace.WithNoTouchTriggerConsecutive(1),
			chantrace.WithNoTouchTriggerWindow(25*time.Millisecond),
			chantrace.WithNoTouchCooldown(20*time.Millisecond),
			chantrace.WithNoTouchBlockProfileRate(1),
			chantrace.WithNoTouchMutexProfileFraction(1),
		),
	)

	waitForNoTouchReport(t, 2*time.Second, func(report chantrace.NoTouchSnapshot) bool {
		return len(report.RecentIncidents) > 0
	})

	req := httptest.NewRequest("GET", "/debug/chantrace/notouch", nil)
	rec := httptest.NewRecorder()
	handleNoTouch(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}

	var report chantrace.NoTouchSnapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if !report.Enabled {
		t.Fatal("expected enabled report")
	}
	if len(report.RecentIncidents) == 0 {
		t.Fatal("expected recent incidents in notouch response")
	}
}

func TestHandleReportReturnsDisabledDefaults(t *testing.T) {
	chantrace.Shutdown()

	req := httptest.NewRequest("GET", "/debug/chantrace/report", nil)
	rec := httptest.NewRecorder()
	handleReport(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var report chantrace.NoTouchCompactReport
	if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if report.Enabled {
		t.Fatal("expected disabled report")
	}
	if report.Mode != "disabled" {
		t.Fatalf("Mode = %q, want disabled", report.Mode)
	}
	if report.TriggerActive {
		t.Fatal("expected TriggerActive = false")
	}
	if len(report.RecentIncidents) != 0 {
		t.Fatalf("len(RecentIncidents) = %d, want 0", len(report.RecentIncidents))
	}
}

func TestHandleIndexContainsDashboardHooks(t *testing.T) {
	req := httptest.NewRequest("GET", "/debug/chantrace/", nil)
	rec := httptest.NewRecorder()
	handleIndex(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, needle := range []string{
		`/debug/chantrace/report`,
		`id="incidents"`,
		`startDashboardPolling`,
		`/debug/chantrace/notouch`,
	} {
		if !strings.Contains(body, needle) {
			t.Fatalf("index missing %q", needle)
		}
	}
}
