package chantrace

import (
	"context"
	"testing"
	"time"
)

func waitForReportCondition(t *testing.T, timeout time.Duration, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if fn() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for analyzer condition")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestAnalyzerDetectsBlockedSend(t *testing.T) {
	analyzer := NewAnalyzer(
		WithAnalyzerBlockedThreshold(2 * time.Millisecond),
	)
	Enable(WithBackend(analyzer))
	t.Cleanup(Shutdown)

	ch := Make[int]("analyzer-blocked-send")
	done := make(chan struct{})

	go func() {
		defer close(done)
		Send(ch, 1)
	}()

	waitForReportCondition(t, time.Second, func() bool {
		r := analyzer.Report()
		for _, b := range r.Blocked {
			if b.Kind == ChanSendStart && b.ChannelName == "analyzer-blocked-send" {
				return true
			}
		}
		return false
	})

	if v := Recv[int](ch); v != 1 {
		t.Fatalf("Recv = %d, want 1", v)
	}
	<-done

	waitForReportCondition(t, time.Second, func() bool {
		r := analyzer.Report()
		for _, b := range r.Blocked {
			if b.ChannelName == "analyzer-blocked-send" {
				return false
			}
		}
		return true
	})
}

func TestAnalyzerDetectsLeakedGoroutine(t *testing.T) {
	analyzer := NewAnalyzer(
		WithAnalyzerLeakThreshold(2 * time.Millisecond),
	)
	Enable(WithBackend(analyzer))
	t.Cleanup(Shutdown)

	release := make(chan struct{})
	started := make(chan struct{})

	Go(context.Background(), "analyzer-leak", func(_ context.Context) {
		close(started)
		<-release
	})
	<-started

	waitForReportCondition(t, time.Second, func() bool {
		r := analyzer.Report()
		for _, g := range r.Leaked {
			if g.Label == "analyzer-leak" {
				return true
			}
		}
		return false
	})

	close(release)

	waitForReportCondition(t, time.Second, func() bool {
		r := analyzer.Report()
		for _, g := range r.Leaked {
			if g.Label == "analyzer-leak" {
				return false
			}
		}
		return true
	})
}

func TestAnalyzerTraceLostInvalidatesInflightState(t *testing.T) {
	analyzer := NewAnalyzer(WithAnalyzerBlockedThreshold(0))
	now := time.Now().Add(-time.Second).UnixNano()

	analyzer.HandleEvent(Event{
		Kind:        ChanSendStart,
		OpID:        42,
		Timestamp:   now,
		ChannelName: "op",
	})

	report := analyzer.Report()
	if len(report.Blocked) != 1 {
		t.Fatalf("blocked count = %d, want 1", len(report.Blocked))
	}

	analyzer.HandleEvent(Event{
		Kind:      TraceLost,
		Timestamp: time.Now().UnixNano(),
		Dropped:   7,
	})

	report = analyzer.Report()
	if !report.StateUncertain {
		t.Fatal("expected state_uncertain=true after TraceLost")
	}
	if report.DroppedEvents != 7 {
		t.Fatalf("dropped_events = %d, want 7", report.DroppedEvents)
	}
	if len(report.Blocked) != 0 {
		t.Fatalf("blocked count = %d, want 0 after TraceLost invalidation", len(report.Blocked))
	}
}

func TestAnalyzerReportChannelWaitsAndGraph(t *testing.T) {
	analyzer := NewAnalyzer(WithAnalyzerBlockedThreshold(0))
	now := time.Now().Add(-time.Second).UnixNano()

	analyzer.HandleEvent(Event{
		Kind:        ChanSendStart,
		OpID:        1,
		Timestamp:   now,
		GoroutineID: 10,
		ChannelID:   100,
		ChannelName: "jobs",
		ValueType:   "int",
	})
	analyzer.HandleEvent(Event{
		Kind:        ChanRecvStart,
		OpID:        2,
		Timestamp:   now,
		GoroutineID: 11,
		ChannelID:   100,
		ChannelName: "jobs",
		ValueType:   "int",
	})
	analyzer.HandleEvent(Event{
		Kind:        ChanRangeStart,
		OpID:        3,
		Timestamp:   now,
		GoroutineID: 12,
		ChannelID:   100,
		ChannelName: "jobs",
		ValueType:   "int",
	})

	report := analyzer.Report()
	if len(report.ChannelWaits) != 1 {
		t.Fatalf("channel_waits count = %d, want 1", len(report.ChannelWaits))
	}
	wait := report.ChannelWaits[0]
	if wait.ChannelID != 100 {
		t.Fatalf("channel_waits[0].ChannelID = %d, want 100", wait.ChannelID)
	}
	if len(wait.Senders) != 1 || wait.Senders[0] != 10 {
		t.Fatalf("senders = %v, want [10]", wait.Senders)
	}
	if len(wait.Receivers) != 1 || wait.Receivers[0] != 11 {
		t.Fatalf("receivers = %v, want [11]", wait.Receivers)
	}
	if len(wait.Rangers) != 1 || wait.Rangers[0] != 12 {
		t.Fatalf("rangers = %v, want [12]", wait.Rangers)
	}

	if len(report.WaitGraph.Nodes) != 4 {
		t.Fatalf("wait graph node count = %d, want 4", len(report.WaitGraph.Nodes))
	}
	if !hasGraphEdge(report.WaitGraph.Edges, "g:10", "ch:100", "wait-send") {
		t.Fatal("missing wait-send edge g:10 -> ch:100")
	}
	if !hasGraphEdge(report.WaitGraph.Edges, "g:11", "ch:100", "wait-recv") {
		t.Fatal("missing wait-recv edge g:11 -> ch:100")
	}
	if !hasGraphEdge(report.WaitGraph.Edges, "g:12", "ch:100", "wait-range") {
		t.Fatal("missing wait-range edge g:12 -> ch:100")
	}
	if !hasGraphEdge(report.WaitGraph.Edges, "ch:100", "g:11", "potential-recv-counterpart") {
		t.Fatal("missing counterpart edge ch:100 -> g:11")
	}
	if !hasGraphEdge(report.WaitGraph.Edges, "ch:100", "g:10", "potential-send-counterpart") {
		t.Fatal("missing counterpart edge ch:100 -> g:10")
	}
	if len(report.Deadlocks) != 1 {
		t.Fatalf("deadlock count = %d, want 1", len(report.Deadlocks))
	}
	if report.Deadlocks[0].Confidence != "high" {
		t.Fatalf("deadlock confidence = %q, want high", report.Deadlocks[0].Confidence)
	}
	if len(report.Deadlocks[0].Goroutines) != 3 {
		t.Fatalf("deadlock goroutines = %v, want [10 11 12]", report.Deadlocks[0].Goroutines)
	}
}

func TestAnalyzerNoDeadlockForSingleBlockedSend(t *testing.T) {
	analyzer := NewAnalyzer(WithAnalyzerBlockedThreshold(0))
	now := time.Now().Add(-time.Second).UnixNano()

	analyzer.HandleEvent(Event{
		Kind:        ChanSendStart,
		OpID:        1,
		Timestamp:   now,
		GoroutineID: 10,
		ChannelID:   100,
		ChannelName: "jobs",
		ValueType:   "int",
	})

	report := analyzer.Report()
	if len(report.Deadlocks) != 0 {
		t.Fatalf("deadlock count = %d, want 0", len(report.Deadlocks))
	}
}

func hasGraphEdge(edges []WaitGraphEdge, from, to, relation string) bool {
	for _, e := range edges {
		if e.FromID == from && e.ToID == to && e.Relation == relation {
			return true
		}
	}
	return false
}
