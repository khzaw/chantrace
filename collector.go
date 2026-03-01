package chantrace

import (
	"sync"
	"sync/atomic"
	"time"
)

const ringSize = 1 << 16 // 64K events

const defaultBufSize = 16384

// opIDSeq generates unique IDs for correlating Start/Done event pairs.
var opIDSeq atomic.Uint64

func nextOpID() uint64 {
	return opIDSeq.Add(1)
}

type collector struct {
	mu       sync.Mutex
	ring     []Event
	pos      uint64
	backends []Backend

	eventCh chan Event    // buffered channel for async backend dispatch
	doneCh  chan struct{} // signals drain goroutine to exit
	wg      sync.WaitGroup
	bufSize int // configurable buffer size for eventCh
	dropped atomic.Uint64
}

var defaultCollector = &collector{
	ring: make([]Event, ringSize),
}

// emit writes an event to the ring buffer (synchronous, under lock)
// and sends it to the drain goroutine for backend dispatch (async, non-blocking).
// If the async buffer is full, the event is dropped from backend dispatch
// but preserved in the ring buffer. A TraceLost event is synthesized by the
// drain goroutine to notify backends.
func (c *collector) emit(e Event) {
	c.mu.Lock()
	c.ring[c.pos%uint64(len(c.ring))] = e
	c.pos++
	ch := c.eventCh
	c.mu.Unlock()

	if ch != nil {
		select {
		case ch <- e:
		default:
			c.dropped.Add(1)
		}
	}
}

// start launches the background drain goroutine that dispatches events to backends.
func (c *collector) start() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.eventCh != nil {
		return // already running
	}
	size := c.bufSize
	if size <= 0 {
		size = defaultBufSize
	}
	c.eventCh = make(chan Event, size)
	c.doneCh = make(chan struct{})
	eventCh := c.eventCh
	doneCh := c.doneCh
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		c.drain(eventCh, doneCh)
	}()
}

// stopDrain signals the drain goroutine to finish and waits for it.
func (c *collector) stopDrain() {
	c.mu.Lock()
	done := c.doneCh
	c.eventCh = nil // prevent new sends from emit()
	c.doneCh = nil
	c.mu.Unlock()

	if done != nil {
		close(done)
		c.wg.Wait()
	}
}

// drain reads events from eventCh and dispatches them to backends sequentially.
func (c *collector) drain(eventCh chan Event, done chan struct{}) {
	for {
		select {
		case e := <-eventCh:
			c.dispatch(e)
		case <-done:
			// Drain remaining buffered events before exiting
			for {
				select {
				case e := <-eventCh:
					c.dispatch(e)
				default:
					// Final drop notification
					c.mu.Lock()
					backends := c.backends
					c.mu.Unlock()
					c.notifyDropped(backends)
					return
				}
			}
		}
	}
}

// dispatch sends an event to all backends, recovering from panics so that
// a buggy backend does not kill the drain goroutine.
// Resolves deferred PC → File/Line before dispatching.
func (c *collector) dispatch(e Event) {
	resolveEvent(&e)
	c.mu.Lock()
	backends := c.backends
	c.mu.Unlock()
	if c.dropped.Load() > 0 {
		c.notifyDropped(backends)
	}
	for _, b := range backends {
		c.safeHandleEvent(b, e)
	}
}

// resolveEvent fills File/Line from PC if not already resolved.
func resolveEvent(e *Event) {
	if e.PC != 0 && e.File == "" {
		e.File, e.Line = resolvePC(e.PC)
	}
}

// safeHandleEvent calls b.HandleEvent(e), recovering from any panic so
// the drain goroutine and other backends continue operating.
func (c *collector) safeHandleEvent(b Backend, e Event) {
	defer func() { recover() }()
	b.HandleEvent(e)
}

// notifyDropped checks if events were dropped and synthesizes a TraceLost
// event so backends can invalidate stale state (e.g., unpaired Start events).
func (c *collector) notifyDropped(backends []Backend) {
	if n := c.dropped.Swap(0); n > 0 {
		lost := Event{
			Kind:      TraceLost,
			Timestamp: time.Now().UnixNano(),
			Dropped:   n,
		}
		for _, b := range backends {
			c.safeHandleEvent(b, lost)
		}
	}
}

func (c *collector) addBackend(b Backend) {
	c.mu.Lock()
	c.backends = append(c.backends, b)
	c.mu.Unlock()
}

func (c *collector) closeBackends() {
	c.stopDrain()

	c.mu.Lock()
	backends := c.backends
	c.backends = nil
	c.mu.Unlock()

	for _, b := range backends {
		b.Close()
	}
}

func (c *collector) replaceBackends(backends []Backend) {
	c.stopDrain()

	c.mu.Lock()
	old := c.backends
	c.backends = backends
	c.mu.Unlock()

	for _, b := range old {
		b.Close()
	}
}

// snapshot returns the last n events from the ring buffer.
func (c *collector) snapshot(n int) []Event {
	c.mu.Lock()
	total := c.pos
	if total == 0 {
		c.mu.Unlock()
		return nil
	}
	size := len(c.ring)
	if n <= 0 || n > size {
		n = size
	}
	if uint64(n) > total {
		n = int(total)
	}
	events := make([]Event, n)
	start := total - uint64(n)
	for i := range n {
		events[i] = c.ring[(start+uint64(i))%uint64(size)]
	}
	c.mu.Unlock()

	// Resolve PCs outside the lock to avoid blocking emitters.
	for i := range events {
		resolveEvent(&events[i])
	}
	return events
}

// Snapshot returns the last n traced events.
func Snapshot(n int) []Event {
	return defaultCollector.snapshot(n)
}

func (c *collector) ringPos() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.pos
}
