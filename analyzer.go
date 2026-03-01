package chantrace

import (
	"cmp"
	"slices"
	"sync"
	"time"
)

const (
	defaultBlockedThreshold = 100 * time.Millisecond
	defaultLeakThreshold    = 5 * time.Second
)

// AnalyzerOption configures an Analyzer backend.
type AnalyzerOption func(*Analyzer)

// WithAnalyzerBlockedThreshold sets the minimum in-flight duration required for
// an operation to be reported as blocked.
func WithAnalyzerBlockedThreshold(d time.Duration) AnalyzerOption {
	return func(a *Analyzer) {
		if d >= 0 {
			a.blockedThreshold = d
		}
	}
}

// WithAnalyzerLeakThreshold sets the minimum lifetime required for a spawned
// goroutine to be reported as leaked (still alive).
func WithAnalyzerLeakThreshold(d time.Duration) AnalyzerOption {
	return func(a *Analyzer) {
		if d >= 0 {
			a.leakThreshold = d
		}
	}
}

// AnalyzerReport is the current diagnostic snapshot from Analyzer.
type AnalyzerReport struct {
	Timestamp      int64             `json:"timestamp"`
	Blocked        []BlockedOp       `json:"blocked,omitempty"`
	Leaked         []LeakedGoroutine `json:"leaked,omitempty"`
	DroppedEvents  uint64            `json:"dropped_events"`
	StateUncertain bool              `json:"state_uncertain"`
}

// BlockedOp describes an operation that has started but not completed.
type BlockedOp struct {
	Kind        EventKind `json:"kind"`
	OpID        uint64    `json:"op_id"`
	GoroutineID int64     `json:"goroutine_id"`
	ChannelID   uintptr   `json:"channel_id"`
	ChannelName string    `json:"channel_name"`
	ValueType   string    `json:"value_type"`
	PC          uintptr   `json:"pc"`
	File        string    `json:"file"`
	Line        int       `json:"line"`
	Since       int64     `json:"since"`
	DurationNS  int64     `json:"duration_ns"`
}

// LeakedGoroutine describes a goroutine that was spawned but has not exited.
type LeakedGoroutine struct {
	GoroutineID int64   `json:"goroutine_id"`
	ParentGID   int64   `json:"parent_gid"`
	Label       string  `json:"label"`
	PC          uintptr `json:"pc"`
	File        string  `json:"file"`
	Line        int     `json:"line"`
	Since       int64   `json:"since"`
	DurationNS  int64   `json:"duration_ns"`
}

type rangeWaitKey struct {
	gid int64
	ch  uintptr
	pc  uintptr
}

// Analyzer is a backend that performs active deadlock/leak diagnostics.
//
// It tracks in-flight start/done operation pairs and goroutine spawn/exit
// lifecycles, then produces reports via Report().
type Analyzer struct {
	mu sync.Mutex

	inflight  map[uint64]Event
	rangeWait map[rangeWaitKey]Event
	spawned   map[int64]Event

	blockedThreshold time.Duration
	leakThreshold    time.Duration
	droppedEvents    uint64
	stateUncertain   bool
}

// NewAnalyzer constructs a diagnostic backend.
func NewAnalyzer(opts ...AnalyzerOption) *Analyzer {
	a := &Analyzer{
		inflight:         make(map[uint64]Event),
		rangeWait:        make(map[rangeWaitKey]Event),
		spawned:          make(map[int64]Event),
		blockedThreshold: defaultBlockedThreshold,
		leakThreshold:    defaultLeakThreshold,
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// HandleEvent ingests traced events and updates analyzer state.
func (a *Analyzer) HandleEvent(e Event) {
	a.mu.Lock()
	defer a.mu.Unlock()

	switch e.Kind {
	case ChanSendStart, ChanRecvStart, ChanSelectStart:
		if e.OpID != 0 {
			a.inflight[e.OpID] = e
		}
	case ChanSendDone, ChanRecvDone, ChanSelectDone:
		if e.OpID != 0 {
			delete(a.inflight, e.OpID)
		}
	case ChanRangeStart:
		key := rangeWaitKey{
			gid: e.GoroutineID,
			ch:  e.ChannelID,
			pc:  e.PC,
		}
		a.rangeWait[key] = e
	case ChanRange, ChanRangeDone:
		key := rangeWaitKey{
			gid: e.GoroutineID,
			ch:  e.ChannelID,
			pc:  e.PC,
		}
		delete(a.rangeWait, key)
	case GoSpawn:
		if e.GoroutineID != 0 {
			a.spawned[e.GoroutineID] = e
		}
	case GoExit:
		if e.GoroutineID != 0 {
			delete(a.spawned, e.GoroutineID)
		}
	case TraceLost:
		a.droppedEvents += e.Dropped
		a.stateUncertain = true
		clear(a.inflight)
		clear(a.rangeWait)
	}
}

// Close implements Backend.
func (a *Analyzer) Close() error { return nil }

// Report returns a snapshot of current diagnostics.
func (a *Analyzer) Report() AnalyzerReport {
	now := time.Now().UnixNano()

	a.mu.Lock()

	report := AnalyzerReport{
		Timestamp:      now,
		DroppedEvents:  a.droppedEvents,
		StateUncertain: a.stateUncertain,
	}

	addBlocked := func(e Event) {
		if e.Timestamp == 0 {
			return
		}
		d := now - e.Timestamp
		if d < int64(a.blockedThreshold) {
			return
		}
		report.Blocked = append(report.Blocked, BlockedOp{
			Kind:        e.Kind,
			OpID:        e.OpID,
			GoroutineID: e.GoroutineID,
			ChannelID:   e.ChannelID,
			ChannelName: e.ChannelName,
			ValueType:   e.ValueType,
			PC:          e.PC,
			File:        e.File,
			Line:        e.Line,
			Since:       e.Timestamp,
			DurationNS:  d,
		})
	}
	for _, e := range a.inflight {
		addBlocked(e)
	}
	for _, e := range a.rangeWait {
		addBlocked(e)
	}
	for _, e := range a.spawned {
		if e.Timestamp == 0 {
			continue
		}
		d := now - e.Timestamp
		if d < int64(a.leakThreshold) {
			continue
		}
		report.Leaked = append(report.Leaked, LeakedGoroutine{
			GoroutineID: e.GoroutineID,
			ParentGID:   e.ParentGID,
			Label:       e.GoLabel,
			PC:          e.PC,
			File:        e.File,
			Line:        e.Line,
			Since:       e.Timestamp,
			DurationNS:  d,
		})
	}

	a.mu.Unlock()

	slices.SortFunc(report.Blocked, func(a, b BlockedOp) int {
		if c := cmp.Compare(b.DurationNS, a.DurationNS); c != 0 {
			return c
		}
		return cmp.Compare(a.OpID, b.OpID)
	})
	slices.SortFunc(report.Leaked, func(a, b LeakedGoroutine) int {
		if c := cmp.Compare(b.DurationNS, a.DurationNS); c != 0 {
			return c
		}
		return cmp.Compare(a.GoroutineID, b.GoroutineID)
	})

	return report
}
