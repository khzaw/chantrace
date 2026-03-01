package chantrace

import (
	"context"
	"time"
)

// Go launches a traced goroutine with the given label.
// The context carries the parent goroutine ID for building spawn trees.
// The child function receives a new context containing its own goroutine ID,
// retrievable via GoID.
func Go(ctx context.Context, label string, fn func(ctx context.Context)) {
	if !enabled.Load() {
		go fn(ctx)
		return
	}

	parentGID := currentRuntimeGID()
	if parentGID == 0 {
		parentGID = GoID(ctx)
	}
	pc := maybeCapturePC()
	childTraceID := nextGoroutineID()
	childCtx := context.WithValue(ctx, goidKey, childTraceID)

	go func() {
		childGID := currentRuntimeGID()
		if childGID == 0 {
			childGID = childTraceID
		}
		defaultCollector.emit(Event{
			Kind:        GoSpawn,
			Timestamp:   time.Now().UnixNano(),
			GoroutineID: childGID,
			ParentGID:   parentGID,
			GoLabel:     label,
			PC:          pc,
		})

		defer func() {
			if enabled.Load() {
				defaultCollector.emit(Event{
					Kind:        GoExit,
					Timestamp:   time.Now().UnixNano(),
					GoroutineID: childGID,
					GoLabel:     label,
					PC:          pc,
				})
			}
		}()

		fn(childCtx)
	}()
}
