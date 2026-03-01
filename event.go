package chantrace

// EventKind identifies the type of traced operation.
type EventKind uint8

const (
	ChanMake EventKind = iota
	ChanRegister
	ChanSendStart
	ChanSendDone
	ChanRecvStart
	ChanRecvDone
	ChanClose
	ChanSelectStart
	ChanSelectDone
	ChanRangeStart
	ChanRange
	ChanRangeDone
	GoSpawn
	GoExit
	TraceLost // emitted when async backend dispatch drops events
)

var eventKindNames = [...]string{
	ChanMake:        "make",
	ChanRegister:    "register",
	ChanSendStart:   "send→",
	ChanSendDone:    "send✓",
	ChanRecvStart:   "recv→",
	ChanRecvDone:    "recv✓",
	ChanClose:       "close",
	ChanSelectStart: "select→",
	ChanSelectDone:  "select✓",
	ChanRangeStart:  "range→",
	ChanRange:       "range",
	ChanRangeDone:   "range-done",
	GoSpawn:         "go-spawn",
	GoExit:          "go-exit",
	TraceLost:       "trace-lost",
}

func (k EventKind) String() string {
	if int(k) < len(eventKindNames) {
		return eventKindNames[k]
	}
	return "unknown"
}

// Event represents a single traced channel or goroutine operation.
type Event struct {
	Kind        EventKind
	Timestamp   int64 // UnixNano
	GoroutineID int64
	ChannelID   uintptr
	ChannelName string
	ValueType   string // e.g. "int", "main.Order"
	ValueStr    string // fmt.Sprintf, truncated
	BufLen      int    // len(ch) after op
	BufCap      int    // cap(ch)
	OpID        uint64 // correlates Start/Done pairs
	RecvOK      bool   // whether channel was open on receive
	Dropped     uint64 // number of events dropped; only nonzero for TraceLost
	SelectIndex int    // which case fired; only meaningful for ChanSelectDone
	ParentGID   int64  // for GoSpawn
	GoLabel     string
	PC          uintptr // raw program counter; resolved to File/Line lazily
	File        string
	Line        int
}

// Backend receives traced events for output or processing.
type Backend interface {
	HandleEvent(Event)
	Close() error
}
