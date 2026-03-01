package chantrace

import (
	"sync"
	"testing"
	"time"
)

func setupTracing(t *testing.T) *recordingBackend {
	t.Helper()
	rec := &recordingBackend{}
	Enable(WithBackend(rec))
	t.Cleanup(Shutdown)
	return rec
}

func TestMake(t *testing.T) {
	rec := setupTracing(t)

	ch := Make[int]("test-make", 5)
	if cap(ch) != 5 {
		t.Errorf("cap = %d, want 5", cap(ch))
	}

	events := waitForEvents(rec, 1, time.Second)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	e := events[0]
	if e.Kind != ChanMake {
		t.Errorf("Kind = %v, want ChanMake", e.Kind)
	}
	if e.ChannelName != "test-make" {
		t.Errorf("ChannelName = %q, want %q", e.ChannelName, "test-make")
	}
	if e.ValueType != "int" {
		t.Errorf("ValueType = %q, want %q", e.ValueType, "int")
	}
	if e.BufCap != 5 {
		t.Errorf("BufCap = %d, want 5", e.BufCap)
	}
}

func TestMakeUnbuffered(t *testing.T) {
	_ = setupTracing(t)
	ch := Make[string]("unbuf")
	if cap(ch) != 0 {
		t.Errorf("cap = %d, want 0", cap(ch))
	}
}

func TestSendRecv(t *testing.T) {
	rec := setupTracing(t)

	ch := Make[int]("sr", 1)

	Send(ch, 42)
	val := Recv[int](ch)

	if val != 42 {
		t.Fatalf("Recv = %d, want 42", val)
	}

	// Make(1) + SendStart(1) + SendDone(1) + RecvStart(1) + RecvDone(1) = 5
	events := waitForEvents(rec, 5, time.Second)

	hasSendStart, hasSendDone := false, false
	hasRecvStart, hasRecvDone := false, false
	for _, e := range events {
		switch e.Kind {
		case ChanSendStart:
			hasSendStart = true
			if e.ValueStr != "42" {
				t.Errorf("SendStart ValueStr = %q, want %q", e.ValueStr, "42")
			}
		case ChanSendDone:
			hasSendDone = true
		case ChanRecvStart:
			hasRecvStart = true
		case ChanRecvDone:
			hasRecvDone = true
			if e.ValueStr != "42" {
				t.Errorf("RecvDone ValueStr = %q, want %q", e.ValueStr, "42")
			}
		}
	}
	if !hasSendStart {
		t.Error("missing ChanSendStart event")
	}
	if !hasSendDone {
		t.Error("missing ChanSendDone event")
	}
	if !hasRecvStart {
		t.Error("missing ChanRecvStart event")
	}
	if !hasRecvDone {
		t.Error("missing ChanRecvDone event")
	}
}

func TestSendRecvGoroutineID(t *testing.T) {
	rec := setupTracing(t)

	ch := Make[int]("gid-send-recv", 1)
	Send(ch, 11)
	Recv[int](ch)

	events := waitForEvents(rec, 5, time.Second)

	var sendStart, sendDone, recvStart, recvDone *Event
	for i, e := range events {
		if e.ChannelName != "gid-send-recv" {
			continue
		}
		switch e.Kind {
		case ChanSendStart:
			sendStart = &events[i]
		case ChanSendDone:
			sendDone = &events[i]
		case ChanRecvStart:
			recvStart = &events[i]
		case ChanRecvDone:
			recvDone = &events[i]
		}
	}

	if sendStart == nil || sendDone == nil || recvStart == nil || recvDone == nil {
		t.Fatalf("missing expected send/recv events for channel %q", "gid-send-recv")
	}

	if sendStart.GoroutineID <= 0 || sendDone.GoroutineID <= 0 ||
		recvStart.GoroutineID <= 0 || recvDone.GoroutineID <= 0 {
		t.Fatalf("all send/recv events should have GoroutineID > 0: start=%d done=%d recvStart=%d recvDone=%d",
			sendStart.GoroutineID, sendDone.GoroutineID, recvStart.GoroutineID, recvDone.GoroutineID)
	}

	if sendStart.GoroutineID != sendDone.GoroutineID {
		t.Fatalf("send start/done goroutine mismatch: %d != %d", sendStart.GoroutineID, sendDone.GoroutineID)
	}
	if recvStart.GoroutineID != recvDone.GoroutineID {
		t.Fatalf("recv start/done goroutine mismatch: %d != %d", recvStart.GoroutineID, recvDone.GoroutineID)
	}
}

func TestSendRecvOpIDCorrelation(t *testing.T) {
	rec := setupTracing(t)

	ch := Make[int]("opid", 1)
	Send(ch, 1)
	Recv[int](ch)

	events := waitForEvents(rec, 5, time.Second)

	var sendStartOpID, sendDoneOpID uint64
	var recvStartOpID, recvDoneOpID uint64
	for _, e := range events {
		switch e.Kind {
		case ChanSendStart:
			sendStartOpID = e.OpID
		case ChanSendDone:
			sendDoneOpID = e.OpID
		case ChanRecvStart:
			recvStartOpID = e.OpID
		case ChanRecvDone:
			recvDoneOpID = e.OpID
		}
	}

	if sendStartOpID == 0 {
		t.Fatal("SendStart OpID should be > 0")
	}
	if sendStartOpID != sendDoneOpID {
		t.Errorf("Send OpID mismatch: start=%d done=%d", sendStartOpID, sendDoneOpID)
	}
	if recvStartOpID != recvDoneOpID {
		t.Errorf("Recv OpID mismatch: start=%d done=%d", recvStartOpID, recvDoneOpID)
	}
	if sendStartOpID == recvStartOpID {
		t.Error("Send and Recv should have different OpIDs")
	}
}

func TestRecvOk(t *testing.T) {
	rec := setupTracing(t)

	ch := Make[int]("ok-test", 1)
	Send(ch, 99)

	val, ok := RecvOk[int](ch)
	if !ok || val != 99 {
		t.Fatalf("RecvOk = (%d, %v), want (99, true)", val, ok)
	}

	// Make + SendStart + SendDone + RecvStart + RecvDone = 5
	events := waitForEvents(rec, 5, time.Second)

	for _, e := range events {
		if e.Kind == ChanRecvDone && e.ChannelName == "ok-test" {
			if !e.RecvOK {
				t.Error("expected RecvOK=true on open channel")
			}
		}
	}

	Close(ch)

	// RecvOk on closed channel should return zero value and false
	ch2 := Make[int]("ok-test2", 1)
	Close(ch2)
	val, ok = RecvOk[int](ch2)
	if ok {
		t.Error("expected ok=false on closed channel")
	}
	if val != 0 {
		t.Errorf("expected zero value, got %d", val)
	}
}

func TestClose(t *testing.T) {
	rec := setupTracing(t)

	ch := Make[int]("close-test", 5)
	Close(ch)

	// Make + Close = 2
	events := waitForEvents(rec, 2, time.Second)

	hasClose := false
	for _, e := range events {
		if e.Kind == ChanClose {
			hasClose = true
			if e.ChannelName != "close-test" {
				t.Errorf("Close ChannelName = %q, want %q", e.ChannelName, "close-test")
			}
		}
	}
	if !hasClose {
		t.Error("missing ChanClose event")
	}

	// Verify channel is actually closed
	_, ok := <-ch
	if ok {
		t.Error("channel should be closed")
	}
}

func TestCloseDoubleCloseDoesNotEmitSpuriousEvent(t *testing.T) {
	rec := setupTracing(t)

	ch := Make[int]("double-close", 1)
	Close(ch)

	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("expected panic on double close")
			}
		}()
		Close(ch) // should panic and should not emit ChanClose
	}()

	events := waitForEvents(rec, 2, time.Second) // Make + first Close
	if len(events) != 2 {
		t.Fatalf("event count = %d, want 2", len(events))
	}

	closeCount := 0
	for _, e := range events {
		if e.Kind == ChanClose && e.ChannelName == "double-close" {
			closeCount++
		}
	}
	if closeCount != 1 {
		t.Fatalf("close event count = %d, want 1", closeCount)
	}
}

func TestCloseNilChannelDoesNotEmitEvent(t *testing.T) {
	rec := setupTracing(t)

	var ch chan int
	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("expected panic when closing nil channel")
			}
		}()
		Close(ch)
	}()

	time.Sleep(10 * time.Millisecond)
	events := rec.getEvents()
	if len(events) != 0 {
		t.Fatalf("unexpected events after failed nil close: got %d", len(events))
	}
}

func TestRange(t *testing.T) {
	rec := setupTracing(t)

	ch := Make[int]("range-test", 3)
	Send(ch, 1)
	Send(ch, 2)
	Send(ch, 3)
	Close(ch)

	var vals []int
	for v := range Range[int](ch) {
		vals = append(vals, v)
	}

	if len(vals) != 3 || vals[0] != 1 || vals[1] != 2 || vals[2] != 3 {
		t.Errorf("Range values = %v, want [1 2 3]", vals)
	}

	// Make(1) + Send*3(Start+Done=6) + Close(1) + RangeStart*4(4) + Range*3(3) + RangeDone(1) = 16
	events := waitForEvents(rec, 16, time.Second)

	rangeStartCount := 0
	rangeCount := 0
	rangeDone := false
	for _, e := range events {
		if e.Kind == ChanRangeStart {
			rangeStartCount++
		}
		if e.Kind == ChanRange {
			rangeCount++
		}
		if e.Kind == ChanRangeDone {
			rangeDone = true
		}
	}
	if rangeStartCount != 4 {
		t.Errorf("range start event count = %d, want 4", rangeStartCount)
	}
	if rangeCount != 3 {
		t.Errorf("range event count = %d, want 3", rangeCount)
	}
	if !rangeDone {
		t.Error("missing ChanRangeDone event")
	}
}

func TestRangeStartBeforeValue(t *testing.T) {
	rec := setupTracing(t)

	ch := Make[int]("range-wait", 0)
	done := make(chan struct{})

	go func() {
		defer close(done)
		time.Sleep(5 * time.Millisecond)
		Send(ch, 42)
		Close(ch)
	}()

	var values []int
	for v := range Range[int](ch) {
		values = append(values, v)
	}
	<-done

	if len(values) != 1 || values[0] != 42 {
		t.Fatalf("Range values = %v, want [42]", values)
	}

	// Make + SendStart + SendDone + Close + RangeStart*2 + Range + RangeDone = 8
	events := waitForEvents(rec, 8, time.Second)

	startIdx := -1
	valueIdx := -1
	var startGID int64
	var valueGID int64
	for i, e := range events {
		if e.ChannelName != "range-wait" {
			continue
		}
		if e.Kind == ChanRangeStart && startIdx == -1 {
			startIdx = i
			startGID = e.GoroutineID
		}
		if e.Kind == ChanRange && valueIdx == -1 {
			valueIdx = i
			valueGID = e.GoroutineID
		}
	}

	if startIdx == -1 {
		t.Fatal("missing ChanRangeStart event")
	}
	if valueIdx == -1 {
		t.Fatal("missing ChanRange event")
	}
	if startIdx > valueIdx {
		t.Fatalf("ChanRangeStart index = %d, ChanRange index = %d; want start before value", startIdx, valueIdx)
	}
	if startGID <= 0 || valueGID <= 0 {
		t.Fatalf("range events should have GoroutineID > 0: start=%d value=%d", startGID, valueGID)
	}
	if startGID != valueGID {
		t.Fatalf("range start/value goroutine mismatch: %d != %d", startGID, valueGID)
	}
}

func TestNativeGoHasGoroutineID(t *testing.T) {
	rec := setupTracing(t)

	ch := Make[int]("native-go-gid", 0)
	done := make(chan struct{})

	go func() {
		defer close(done)
		Send(ch, 99)
	}()

	got := Recv[int](ch)
	if got != 99 {
		t.Fatalf("Recv = %d, want 99", got)
	}
	<-done

	events := waitForEvents(rec, 5, time.Second)

	var sendGID int64
	var recvGID int64
	for _, e := range events {
		if e.ChannelName != "native-go-gid" {
			continue
		}
		if e.Kind == ChanSendStart {
			sendGID = e.GoroutineID
		}
		if e.Kind == ChanRecvStart {
			recvGID = e.GoroutineID
		}
	}

	if sendGID <= 0 {
		t.Fatalf("send event GoroutineID = %d, want > 0", sendGID)
	}
	if recvGID <= 0 {
		t.Fatalf("recv event GoroutineID = %d, want > 0", recvGID)
	}
	if sendGID == recvGID {
		t.Fatalf("expected different goroutine IDs for sender and receiver, both were %d", sendGID)
	}
}

func TestRegister(t *testing.T) {
	rec := setupTracing(t)

	ch := make(chan float64, 8)
	Register(ch, "external")

	Send(ch, 3.14)
	val := Recv[float64](ch)

	if val != 3.14 {
		t.Fatalf("Recv = %f, want 3.14", val)
	}

	// Register + SendStart + SendDone + RecvStart + RecvDone = 5
	events := waitForEvents(rec, 5, time.Second)

	hasSend := false
	for _, e := range events {
		if e.Kind == ChanSendStart && e.ChannelName == "external" {
			hasSend = true
		}
	}
	if !hasSend {
		t.Error("Send on registered channel should have name 'external'")
	}
}

func TestDisabledTracing(t *testing.T) {
	// Explicitly ensure tracing is off
	Shutdown()
	t.Cleanup(Shutdown)

	ch := make(chan int, 1)

	// Native operations work
	ch <- 42
	val := <-ch
	if val != 42 {
		t.Fatalf("got %d, want 42", val)
	}

	// Record ring position before ops
	before := defaultCollector.ringPos()

	// Traced operations should still work (just no events emitted)
	Send(ch, 99)
	got := Recv[int](ch)
	if got != 99 {
		t.Fatalf("Recv = %d, want 99", got)
	}

	// Verify no new events were emitted
	after := defaultCollector.ringPos()
	if after != before {
		t.Errorf("expected no events when disabled, ring advanced by %d", after-before)
	}
}

func TestDisablePCCapture(t *testing.T) {
	rec := &recordingBackend{}
	Enable(
		WithBackend(rec),
		WithPCCapture(false),
	)
	t.Cleanup(Shutdown)

	ch := Make[int]("no-pc", 1)
	Send(ch, 5)
	Recv[int](ch)

	events := waitForEvents(rec, 5, time.Second)
	if len(events) < 5 {
		t.Fatalf("expected at least 5 events, got %d", len(events))
	}

	for _, e := range events {
		if e.ChannelName != "no-pc" {
			continue
		}
		if e.PC != 0 {
			t.Fatalf("event %s had PC=%d, want 0 when pc capture disabled", e.Kind, e.PC)
		}
		if e.File != "" || e.Line != 0 {
			t.Fatalf("event %s resolved file/line unexpectedly: %q:%d", e.Kind, e.File, e.Line)
		}
	}
}

func TestPCSampleEveryTwo(t *testing.T) {
	rec := &recordingBackend{}
	Enable(
		WithBackend(rec),
		WithPCSampleEvery(2),
	)
	t.Cleanup(Shutdown)

	ch := Make[int]("pc-sample-2", 1)
	Send(ch, 1)
	Recv[int](ch)

	events := waitForEvents(rec, 5, time.Second)

	var makeEvent, sendStart, sendDone, recvStart, recvDone *Event
	for i, e := range events {
		if e.ChannelName != "pc-sample-2" {
			continue
		}
		switch e.Kind {
		case ChanMake:
			makeEvent = &events[i]
		case ChanSendStart:
			sendStart = &events[i]
		case ChanSendDone:
			sendDone = &events[i]
		case ChanRecvStart:
			recvStart = &events[i]
		case ChanRecvDone:
			recvDone = &events[i]
		}
	}

	if makeEvent == nil || sendStart == nil || sendDone == nil || recvStart == nil || recvDone == nil {
		t.Fatalf("missing expected events for channel %q", "pc-sample-2")
	}

	if makeEvent.PC == 0 {
		t.Fatal("make event should capture PC with sample every 2")
	}
	if sendStart.PC != 0 || sendDone.PC != 0 {
		t.Fatalf("send events should be unsampled with sample every 2: start=%d done=%d", sendStart.PC, sendDone.PC)
	}
	if recvStart.PC == 0 || recvDone.PC == 0 {
		t.Fatalf("recv events should capture sampled PC with sample every 2: start=%d done=%d", recvStart.PC, recvDone.PC)
	}
}

func TestBlockedDetection(t *testing.T) {
	rec := setupTracing(t)

	ch := Make[int]("blocked-test") // unbuffered

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(5 * time.Millisecond)
		Recv[int](ch)
	}()

	Send(ch, 1) // should block until receiver is ready
	wg.Wait()

	// Make(1) + SendStart + SendDone + RecvStart + RecvDone = 5
	events := waitForEvents(rec, 5, time.Second)

	var sendStart, sendDone *Event
	for i, e := range events {
		if e.Kind == ChanSendStart && e.ChannelName == "blocked-test" {
			sendStart = &events[i]
		}
		if e.Kind == ChanSendDone && e.ChannelName == "blocked-test" {
			sendDone = &events[i]
		}
	}

	if sendStart == nil {
		t.Fatal("missing ChanSendStart event")
	}
	if sendDone == nil {
		t.Fatal("missing ChanSendDone event")
	}

	blockedNs := sendDone.Timestamp - sendStart.Timestamp
	if blockedNs < int64(time.Millisecond) {
		t.Errorf("blocking duration = %d ns, expected >= 1ms", blockedNs)
	}
}

func TestTruncate(t *testing.T) {
	short := truncate("hello", 10)
	if short != "hello" {
		t.Errorf("truncate short = %q, want %q", short, "hello")
	}

	long := truncate("abcdefghij", 7)
	if long != "abcd..." {
		t.Errorf("truncate long = %q, want %q", long, "abcd...")
	}

	// maxRunes < 3 should clamp
	tiny := truncate("hello", 1)
	if tiny != "..." {
		t.Errorf("truncate tiny = %q, want %q", tiny, "...")
	}
}
