package chantrace

import (
	"fmt"
	"os"
	"sync"
	"sync/atomic"
)

var (
	enabled        atomic.Bool
	snapshotValues atomic.Bool
	pcCapture      atomic.Bool
	pcSampleEvery  atomic.Uint32
	pcSampleSeq    atomic.Uint64
	shutdownMu     sync.Mutex
)

func init() {
	pcCapture.Store(true)
	pcSampleEvery.Store(1)
	if mode := os.Getenv("CHANTRACE"); mode != "" {
		autoEnable(mode)
	}
}

func autoEnable(mode string) {
	switch mode {
	case "tui":
		Enable(WithTUI())
	case "web":
		Enable(WithWeb(""))
	case "notouch":
		Enable(WithNoTouch())
	default:
		Enable(WithLogStream())
	}
}

// Option configures the tracer.
type Option func(*traceConfig)

type traceConfig struct {
	backends      []Backend
	bufSize       int
	snapValues    *bool
	pcCapture     *bool
	pcSampleEvery *uint32
	noTouch       *NoTouchConfig
}

// WithLogStream enables colored log output to stderr.
func WithLogStream() Option {
	return func(c *traceConfig) {
		c.backends = append(c.backends, newLogStream())
	}
}

// WithTUI enables the terminal UI dashboard.
// Import github.com/khzaw/chantrace/backend/tui for full TUI support.
// Falls back to logstream if TUI package is not imported.
func WithTUI() Option {
	return func(c *traceConfig) {
		if f, ok := backendFactories.Load("tui"); ok {
			c.backends = append(c.backends, f.(func() Backend)())
		} else {
			c.backends = append(c.backends, newLogStream())
		}
	}
}

// WithWeb enables the web UI dashboard on the given address (e.g. ":4884").
// Import github.com/khzaw/chantrace/backend/web for full web support.
// Falls back to logstream if web package is not imported.
func WithWeb(addr string) Option {
	return func(c *traceConfig) {
		if f, ok := backendFactories.Load("web"); ok {
			factory := f.(func(string) Backend)
			c.backends = append(c.backends, factory(addr))
		} else {
			c.backends = append(c.backends, newLogStream())
		}
	}
}

// WithBackend adds a custom backend.
func WithBackend(b Backend) Option {
	return func(c *traceConfig) {
		c.backends = append(c.backends, b)
	}
}

// WithBufferSize sets the async dispatch buffer (default 16384).
// Overflow drops from backend dispatch but stays in the ring for [Snapshot].
func WithBufferSize(n int) Option {
	return func(c *traceConfig) {
		c.bufSize = n
	}
}

// WithValueSnapshot controls whether values are captured via fmt.Sprintf.
// Default is true. Disable to avoid reflection and String() overhead.
func WithValueSnapshot(on bool) Option {
	return func(c *traceConfig) {
		c.snapValues = &on
	}
}

// WithPCCapture controls whether program counters are captured.
// Default is true. Disable to skip the ~100ns runtime.Callers cost per event.
func WithPCCapture(on bool) Option {
	return func(c *traceConfig) {
		c.pcCapture = &on
	}
}

// WithPCSampleEvery captures PCs for one out of every n traced operations.
// Default is 1 (capture every operation). Values <= 1 disable sampling.
func WithPCSampleEvery(n uint32) Option {
	return func(c *traceConfig) {
		c.pcSampleEvery = &n
	}
}

// WithNoTouch enables low-perturbation runtime sampling with anomaly-triggered
// block/mutex profiling windows. This mode does not require wrapping channel
// operations and is intended for initial debugging passes.
func WithNoTouch(opts ...NoTouchOption) Option {
	return func(c *traceConfig) {
		cfg := defaultNoTouchConfig()
		for _, opt := range opts {
			opt(&cfg)
		}
		c.noTouch = &cfg
	}
}

var backendFactories sync.Map // string → factory function

// RegisterBackendFactory registers a named backend constructor, typically
// called from a backend sub-package's init(). Accepts func() Backend
// or func(string) Backend.
func RegisterBackendFactory(name string, factory any) {
	switch factory.(type) {
	case func() Backend, func(string) Backend:
		backendFactories.Store(name, factory)
	default:
		panic(fmt.Sprintf("chantrace.RegisterBackendFactory: unsupported factory type %T", factory))
	}
}

// Enable starts tracing. Calling again replaces backends.
// Defaults to [WithLogStream] if no backends are specified.
func Enable(opts ...Option) {
	shutdownMu.Lock()
	defer shutdownMu.Unlock()

	cfg := &traceConfig{}
	for _, opt := range opts {
		opt(cfg)
	}
	stopNoTouchLocked()

	if len(cfg.backends) == 0 && cfg.noTouch == nil {
		cfg.backends = append(cfg.backends, newLogStream())
	}
	if cfg.bufSize > 0 {
		defaultCollector.bufSize = cfg.bufSize
	}
	if cfg.snapValues != nil {
		snapshotValues.Store(*cfg.snapValues)
	} else {
		snapshotValues.Store(true)
	}
	if cfg.pcCapture != nil {
		pcCapture.Store(*cfg.pcCapture)
	} else {
		pcCapture.Store(true)
	}
	if cfg.pcSampleEvery != nil && *cfg.pcSampleEvery > 1 {
		pcSampleEvery.Store(*cfg.pcSampleEvery)
	} else {
		pcSampleEvery.Store(1)
	}
	pcSampleSeq.Store(0)
	defaultCollector.replaceBackends(cfg.backends)
	defaultCollector.start()
	enabled.Store(true)
	if cfg.noTouch != nil {
		startNoTouchLocked(*cfg.noTouch)
	}
}

// Shutdown stops tracing and flushes all backends.
func Shutdown() {
	shutdownMu.Lock()
	defer shutdownMu.Unlock()

	enabled.Store(false)
	stopNoTouchLocked()
	defaultCollector.closeBackends()
}

// Enabled reports whether tracing is currently active.
func Enabled() bool {
	return enabled.Load()
}
