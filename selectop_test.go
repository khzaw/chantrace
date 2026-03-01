package chantrace

import (
	"testing"
	"time"
)

func TestSelectRecv(t *testing.T) {
	rec := setupTracing(t)

	ch := Make[int]("select-recv", 1)
	Send(ch, 42)

	var got int
	Select(
		OnRecv(ch, func(v int) { got = v }),
	)

	if got != 42 {
		t.Fatalf("OnRecv got %d, want 42", got)
	}

	// Make + SendStart + SendDone + SelectStart + SelectDone = 5
	events := waitForEvents(rec, 5, time.Second)

	hasSelectStart, hasSelectDone := false, false
	for _, e := range events {
		if e.Kind == ChanSelectStart {
			hasSelectStart = true
		}
		if e.Kind == ChanSelectDone {
			hasSelectDone = true
			if e.SelectIndex != 0 {
				t.Errorf("SelectIndex = %d, want 0", e.SelectIndex)
			}
			if e.ChannelName != "select-recv" {
				t.Errorf("ChannelName = %q, want %q", e.ChannelName, "select-recv")
			}
		}
	}
	if !hasSelectStart {
		t.Error("missing ChanSelectStart event")
	}
	if !hasSelectDone {
		t.Error("missing ChanSelectDone event")
	}
}

func TestSelectSend(t *testing.T) {
	rec := setupTracing(t)

	ch := Make[int]("select-send", 1)

	sent := false
	Select(
		OnSend(ch, 99, func() { sent = true }),
	)

	if !sent {
		t.Fatal("OnSend callback not called")
	}

	val := Recv[int](ch)
	if val != 99 {
		t.Fatalf("Recv = %d, want 99", val)
	}

	// Make + SelectStart + SelectDone + RecvStart + RecvDone = 5
	events := waitForEvents(rec, 5, time.Second)

	hasSelectDone := false
	for _, e := range events {
		if e.Kind == ChanSelectDone {
			hasSelectDone = true
		}
	}
	if !hasSelectDone {
		t.Error("missing ChanSelectDone event")
	}
}

func TestSelectDefault(t *testing.T) {
	_ = setupTracing(t)

	ch := Make[int]("select-default") // unbuffered, nothing to recv

	defaultHit := false
	Select(
		OnRecv(ch, func(v int) { t.Error("should not receive") }),
		OnDefault(func() { defaultHit = true }),
	)

	if !defaultHit {
		t.Fatal("default case not hit")
	}
}

func TestSelectMultipleCases(t *testing.T) {
	_ = setupTracing(t)

	ch1 := Make[int]("multi-1", 1)
	ch2 := Make[string]("multi-2", 1)

	Send(ch2, "hello")

	var gotStr string
	Select(
		OnRecv(ch1, func(v int) { t.Error("should not receive from ch1") }),
		OnRecv(ch2, func(v string) { gotStr = v }),
	)

	if gotStr != "hello" {
		t.Fatalf("got %q, want %q", gotStr, "hello")
	}
}

func TestSelectZeroCases(t *testing.T) {
	_ = setupTracing(t)
	// Should return immediately, not deadlock
	Select()
}

func TestSelectSendNilInterface(t *testing.T) {
	_ = setupTracing(t)

	ch := Make[error]("nil-err", 1)

	sent := false
	Select(
		OnSend[error](ch, nil, func() { sent = true }),
	)

	if !sent {
		t.Fatal("OnSend callback not called for nil error")
	}

	val := Recv[error](ch)
	if val != nil {
		t.Fatalf("expected nil error, got %v", val)
	}
}

func TestSelectStartDoneOpID(t *testing.T) {
	rec := setupTracing(t)

	ch := Make[int]("select-opid", 1)
	Send(ch, 1)

	Select(
		OnRecv(ch, func(int) {}),
	)

	// Make + SendStart + SendDone + SelectStart + SelectDone = 5
	events := waitForEvents(rec, 5, time.Second)

	var startOpID, doneOpID uint64
	var startGID, doneGID int64
	for _, e := range events {
		if e.Kind == ChanSelectStart {
			startOpID = e.OpID
			startGID = e.GoroutineID
		}
		if e.Kind == ChanSelectDone {
			doneOpID = e.OpID
			doneGID = e.GoroutineID
		}
	}

	if startOpID == 0 {
		t.Fatal("SelectStart OpID should be > 0")
	}
	if startOpID != doneOpID {
		t.Errorf("Select OpID mismatch: start=%d done=%d", startOpID, doneOpID)
	}
	if startGID <= 0 || doneGID <= 0 {
		t.Fatalf("select events should have GoroutineID > 0: start=%d done=%d", startGID, doneGID)
	}
	if startGID != doneGID {
		t.Fatalf("select start/done goroutine mismatch: %d != %d", startGID, doneGID)
	}
}

func TestSelectRecvOKOpenChannel(t *testing.T) {
	rec := setupTracing(t)

	ch := Make[int]("select-recv-ok-open", 1)
	Send(ch, 7)

	var gotVal int
	var gotOK bool
	Select(
		OnRecvOK(ch, func(v int, ok bool) {
			gotVal = v
			gotOK = ok
		}),
	)

	if gotVal != 7 || !gotOK {
		t.Fatalf("OnRecvOK got (%d, %v), want (7, true)", gotVal, gotOK)
	}

	events := waitForEvents(rec, 5, time.Second)
	for _, e := range events {
		if e.Kind == ChanSelectDone {
			if !e.RecvOK {
				t.Fatalf("ChanSelectDone.RecvOK = %v, want true", e.RecvOK)
			}
			return
		}
	}
	t.Fatal("missing ChanSelectDone event")
}

func TestSelectRecvOKClosedChannel(t *testing.T) {
	rec := setupTracing(t)

	ch := Make[int]("select-recv-ok-closed", 1)
	Close(ch)

	var gotVal int
	var gotOK bool
	Select(
		OnRecvOK(ch, func(v int, ok bool) {
			gotVal = v
			gotOK = ok
		}),
	)

	if gotVal != 0 || gotOK {
		t.Fatalf("OnRecvOK got (%d, %v), want (0, false)", gotVal, gotOK)
	}

	events := waitForEvents(rec, 4, time.Second)
	for _, e := range events {
		if e.Kind == ChanSelectDone {
			if e.RecvOK {
				t.Fatalf("ChanSelectDone.RecvOK = %v, want false", e.RecvOK)
			}
			return
		}
	}
	t.Fatal("missing ChanSelectDone event")
}

func TestSelectPCCaptureDisabled(t *testing.T) {
	rec := &recordingBackend{}
	Enable(
		WithBackend(rec),
		WithPCCapture(false),
	)
	t.Cleanup(Shutdown)

	ch := Make[int]("select-no-pc", 1)
	Send(ch, 3)
	Select(OnRecv(ch, func(int) {}))

	events := waitForEvents(rec, 5, time.Second)
	for _, e := range events {
		if e.Kind != ChanSelectStart && e.Kind != ChanSelectDone {
			continue
		}
		if e.PC != 0 {
			t.Fatalf("%s had PC=%d, want 0 when pc capture disabled", e.Kind, e.PC)
		}
	}
}
