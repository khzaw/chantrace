package chantrace

import (
	"cmp"
	"slices"
	"strconv"
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
	Timestamp      int64              `json:"timestamp"`
	Blocked        []BlockedOp        `json:"blocked,omitempty"`
	Leaked         []LeakedGoroutine  `json:"leaked,omitempty"`
	ChannelWaits   []ChannelWaitState `json:"channel_waits,omitempty"`
	WaitGraph      WaitGraph          `json:"wait_graph"`
	DroppedEvents  uint64             `json:"dropped_events"`
	StateUncertain bool               `json:"state_uncertain"`
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

// ChannelWaitState summarizes blocked waiters by direction for one channel.
type ChannelWaitState struct {
	ChannelID   uintptr `json:"channel_id"`
	ChannelName string  `json:"channel_name"`
	ValueType   string  `json:"value_type"`
	Senders     []int64 `json:"senders,omitempty"`
	Receivers   []int64 `json:"receivers,omitempty"`
	Rangers     []int64 `json:"rangers,omitempty"`
	LongestWait int64   `json:"longest_wait_ns"`
}

// WaitGraph captures the current goroutine↔channel wait relationships.
type WaitGraph struct {
	Nodes []WaitGraphNode `json:"nodes,omitempty"`
	Edges []WaitGraphEdge `json:"edges,omitempty"`
}

// WaitGraphNode is one graph vertex.
type WaitGraphNode struct {
	ID          string  `json:"id"`
	Kind        string  `json:"kind"` // "goroutine" | "channel"
	GoroutineID int64   `json:"goroutine_id,omitempty"`
	ChannelID   uintptr `json:"channel_id,omitempty"`
	ChannelName string  `json:"channel_name,omitempty"`
}

// WaitGraphEdge is one directed relationship in the wait graph.
type WaitGraphEdge struct {
	FromID      string  `json:"from"`
	ToID        string  `json:"to"`
	Relation    string  `json:"relation"`
	GoroutineID int64   `json:"goroutine_id,omitempty"`
	ChannelID   uintptr `json:"channel_id,omitempty"`
	OpID        uint64  `json:"op_id,omitempty"`
	Since       int64   `json:"since"`
	DurationNS  int64   `json:"duration_ns"`
}

type rangeWaitKey struct {
	gid int64
	ch  uintptr
	pc  uintptr
}

// Analyzer is a [Backend] that detects blocked operations and leaked goroutines.
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

// HandleEvent implements [Backend].
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
	report.ChannelWaits, report.WaitGraph = buildWaitGraph(report.Blocked)

	return report
}

func buildWaitGraph(blocked []BlockedOp) ([]ChannelWaitState, WaitGraph) {
	type waitAcc struct {
		id        uintptr
		name      string
		valueType string
		senders   map[int64]struct{}
		receivers map[int64]struct{}
		rangers   map[int64]struct{}
		longestNS int64
	}

	channelWaits := make(map[uintptr]*waitAcc)
	nodes := make(map[string]WaitGraphNode)
	edgeMap := make(map[string]WaitGraphEdge)

	addNode := func(n WaitGraphNode) {
		nodes[n.ID] = n
	}
	addEdge := func(e WaitGraphEdge) {
		key := e.FromID + "->" + e.ToID + ":" + e.Relation
		if old, ok := edgeMap[key]; ok {
			if e.DurationNS > old.DurationNS {
				edgeMap[key] = e
			}
			return
		}
		edgeMap[key] = e
	}

	for _, b := range blocked {
		if b.GoroutineID != 0 {
			addNode(WaitGraphNode{
				ID:          gidNodeID(b.GoroutineID),
				Kind:        "goroutine",
				GoroutineID: b.GoroutineID,
			})
		}

		if b.ChannelID == 0 || b.GoroutineID == 0 {
			continue
		}

		addNode(WaitGraphNode{
			ID:          channelNodeID(b.ChannelID),
			Kind:        "channel",
			ChannelID:   b.ChannelID,
			ChannelName: b.ChannelName,
		})

		acc := channelWaits[b.ChannelID]
		if acc == nil {
			acc = &waitAcc{
				id:        b.ChannelID,
				name:      b.ChannelName,
				valueType: b.ValueType,
				senders:   make(map[int64]struct{}),
				receivers: make(map[int64]struct{}),
				rangers:   make(map[int64]struct{}),
			}
			channelWaits[b.ChannelID] = acc
		}
		if b.DurationNS > acc.longestNS {
			acc.longestNS = b.DurationNS
		}

		relation := ""
		switch b.Kind {
		case ChanSendStart:
			acc.senders[b.GoroutineID] = struct{}{}
			relation = "wait-send"
		case ChanRecvStart:
			acc.receivers[b.GoroutineID] = struct{}{}
			relation = "wait-recv"
		case ChanRangeStart:
			acc.rangers[b.GoroutineID] = struct{}{}
			relation = "wait-range"
		default:
			continue
		}

		addEdge(WaitGraphEdge{
			FromID:      gidNodeID(b.GoroutineID),
			ToID:        channelNodeID(b.ChannelID),
			Relation:    relation,
			GoroutineID: b.GoroutineID,
			ChannelID:   b.ChannelID,
			OpID:        b.OpID,
			Since:       b.Since,
			DurationNS:  b.DurationNS,
		})
	}

	var states []ChannelWaitState
	for _, acc := range channelWaits {
		senders := mapKeys(acc.senders)
		receivers := mapKeys(acc.receivers)
		rangers := mapKeys(acc.rangers)

		state := ChannelWaitState{
			ChannelID:   acc.id,
			ChannelName: acc.name,
			ValueType:   acc.valueType,
			Senders:     senders,
			Receivers:   receivers,
			Rangers:     rangers,
			LongestWait: acc.longestNS,
		}
		states = append(states, state)

		// Potential counterpart relationships on the same channel.
		for range senders {
			for _, recv := range receivers {
				addEdge(WaitGraphEdge{
					FromID:      channelNodeID(acc.id),
					ToID:        gidNodeID(recv),
					Relation:    "potential-recv-counterpart",
					GoroutineID: recv,
					ChannelID:   acc.id,
				})
			}
			for _, ranger := range rangers {
				addEdge(WaitGraphEdge{
					FromID:      channelNodeID(acc.id),
					ToID:        gidNodeID(ranger),
					Relation:    "potential-range-counterpart",
					GoroutineID: ranger,
					ChannelID:   acc.id,
				})
			}
		}
		for range receivers {
			for _, sender := range senders {
				addEdge(WaitGraphEdge{
					FromID:      channelNodeID(acc.id),
					ToID:        gidNodeID(sender),
					Relation:    "potential-send-counterpart",
					GoroutineID: sender,
					ChannelID:   acc.id,
				})
			}
		}
		for range rangers {
			for _, sender := range senders {
				addEdge(WaitGraphEdge{
					FromID:      channelNodeID(acc.id),
					ToID:        gidNodeID(sender),
					Relation:    "potential-send-counterpart",
					GoroutineID: sender,
					ChannelID:   acc.id,
				})
			}
		}
	}

	slices.SortFunc(states, func(a, b ChannelWaitState) int {
		if c := cmp.Compare(a.ChannelID, b.ChannelID); c != 0 {
			return c
		}
		return cmp.Compare(a.ChannelName, b.ChannelName)
	})

	graph := WaitGraph{
		Nodes: mapValues(nodes),
		Edges: mapEdgeValues(edgeMap),
	}
	slices.SortFunc(graph.Nodes, func(a, b WaitGraphNode) int { return cmp.Compare(a.ID, b.ID) })
	slices.SortFunc(graph.Edges, func(a, b WaitGraphEdge) int {
		if c := cmp.Compare(a.FromID, b.FromID); c != 0 {
			return c
		}
		if c := cmp.Compare(a.ToID, b.ToID); c != 0 {
			return c
		}
		return cmp.Compare(a.Relation, b.Relation)
	})

	return states, graph
}

func gidNodeID(gid int64) string {
	return "g:" + strconv.FormatInt(gid, 10)
}

func channelNodeID(ch uintptr) string {
	return "ch:" + strconv.FormatUint(uint64(ch), 10)
}

func mapKeys(m map[int64]struct{}) []int64 {
	out := make([]int64, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func mapValues[T any](m map[string]T) []T {
	out := make([]T, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}

func mapEdgeValues(m map[string]WaitGraphEdge) []WaitGraphEdge {
	out := make([]WaitGraphEdge, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}
