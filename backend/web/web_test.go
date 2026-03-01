package web

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/khzaw/chantrace"
)

func readEventsFromHandler(t *testing.T, b *backend) []chantrace.Event {
	t.Helper()

	req := httptest.NewRequest("GET", "/events", nil)
	rr := httptest.NewRecorder()
	b.handleEvents(rr, req)

	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	var events []chantrace.Event
	if err := json.Unmarshal(rr.Body.Bytes(), &events); err != nil {
		t.Fatalf("unmarshal events: %v", err)
	}
	return events
}

func TestHandleEventsUnderCapacityOrder(t *testing.T) {
	b := &backend{}

	for i := 0; i < 5; i++ {
		b.HandleEvent(chantrace.Event{
			Kind: chantrace.ChanSendStart,
			Line: i,
		})
	}

	events := readEventsFromHandler(t, b)
	if len(events) != 5 {
		t.Fatalf("event count = %d, want 5", len(events))
	}

	for i, e := range events {
		if e.Line != i {
			t.Fatalf("events[%d].Line = %d, want %d", i, e.Line, i)
		}
	}
}

func TestHandleEventsOverCapacityKeepsNewestInOrder(t *testing.T) {
	b := &backend{}

	total := maxBufferedEvents + 10
	for i := 0; i < total; i++ {
		b.HandleEvent(chantrace.Event{
			Kind: chantrace.ChanSendStart,
			Line: i,
		})
	}

	events := readEventsFromHandler(t, b)
	if len(events) != maxBufferedEvents {
		t.Fatalf("event count = %d, want %d", len(events), maxBufferedEvents)
	}

	start := total - maxBufferedEvents
	for i, e := range events {
		want := start + i
		if e.Line != want {
			t.Fatalf("events[%d].Line = %d, want %d", i, e.Line, want)
		}
	}
}
