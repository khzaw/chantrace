package chantrace

import (
	"context"
	"testing"
	"time"
)

func TestGo(t *testing.T) {
	rec := setupTracing(t)

	done := make(chan struct{})
	Go(context.Background(), "test-goroutine", func(_ context.Context) {
		close(done)
	})
	<-done

	// GoExit fires in a defer after fn() returns. Poll until it arrives.
	deadline := time.After(time.Second)
	for {
		events := rec.getEvents()
		hasExit := false
		for _, e := range events {
			if e.Kind == GoExit && e.GoLabel == "test-goroutine" {
				hasExit = true
			}
		}
		if hasExit {
			break
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for GoExit event")
		default:
			time.Sleep(time.Millisecond)
		}
	}

	events := rec.getEvents()
	hasSpawn := false
	for _, e := range events {
		if e.Kind == GoSpawn && e.GoLabel == "test-goroutine" {
			hasSpawn = true
			if e.GoroutineID <= 0 {
				t.Error("GoSpawn GoroutineID should be > 0")
			}
		}
	}
	if !hasSpawn {
		t.Error("missing GoSpawn event")
	}
}

func TestGoParentID(t *testing.T) {
	rec := setupTracing(t)

	done := make(chan struct{})
	ctx := context.Background()

	Go(ctx, "parent", func(ctx context.Context) {
		Go(ctx, "child", func(_ context.Context) {
			close(done)
		})
	})
	<-done

	// Wait for both GoSpawn events
	deadline := time.After(time.Second)
	var parentGID int64
	var childParentGID int64
	foundChild := false
	for {
		events := rec.getEvents()
		parentGID = 0
		childParentGID = 0
		foundChild = false
		for _, e := range events {
			if e.Kind == GoSpawn && e.GoLabel == "parent" {
				parentGID = e.GoroutineID
			}
			if e.Kind == GoSpawn && e.GoLabel == "child" {
				childParentGID = e.ParentGID
				foundChild = true
			}
		}
		if parentGID > 0 && foundChild {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for GoSpawn events: parentGID=%d foundChild=%v",
				parentGID, foundChild)
		default:
			time.Sleep(time.Millisecond)
		}
	}

	if childParentGID != parentGID {
		t.Errorf("child ParentGID = %d, want parent GID %d", childParentGID, parentGID)
	}
}

func TestGoID(t *testing.T) {
	ctx := context.Background()
	if id := GoID(ctx); id != 0 {
		t.Errorf("GoID on background context = %d, want 0", id)
	}

	_ = setupTracing(t)
	done := make(chan struct{})
	var childID int64

	Go(ctx, "id-test", func(ctx context.Context) {
		childID = GoID(ctx)
		close(done)
	})
	<-done

	if childID <= 0 {
		t.Errorf("GoID inside Go() = %d, want > 0", childID)
	}
}

func TestGoPCCaptureDisabled(t *testing.T) {
	rec := &recordingBackend{}
	Enable(
		WithBackend(rec),
		WithPCCapture(false),
	)
	t.Cleanup(Shutdown)

	done := make(chan struct{})
	Go(context.Background(), "no-pc-go", func(_ context.Context) {
		close(done)
	})
	<-done

	events := waitForEvents(rec, 2, time.Second)
	for _, e := range events {
		if e.Kind != GoSpawn && e.Kind != GoExit {
			continue
		}
		if e.PC != 0 {
			t.Fatalf("%s had PC=%d, want 0 when pc capture disabled", e.Kind, e.PC)
		}
	}
}
