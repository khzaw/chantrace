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

// WithBufferSize sets the async event buffer size for backend dispatch.
// Default is 16384. Events that exceed the buffer are dropped from backend
// dispatch but preserved in the ring buffer for Snapshot().
func WithBufferSize(n int) Option {
	return func(c *traceConfig) {
		c.bufSize = n
	}
}

// WithValueSnapshot controls whether values are captured via fmt.Sprintf
// in Send/Recv events. Default is true. Disable in performance-sensitive
// paths where value formatting (reflection, String() methods) is too costly.
func WithValueSnapshot(on bool) Option {
	return func(c *traceConfig) {
		c.snapValues = &on
	}
}

// WithPCCapture controls whether program counters are captured for events.
// Default is true. Disable in high-throughput paths to avoid runtime.Callers
// overhead.
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

// backendFactories allows sub-packages to register backend constructors.
var backendFactories sync.Map // string → factory function

// RegisterBackendFactory registers a named backend factory.
// Used by backend sub-packages in their init() functions.
// Supported factory types: func() Backend, func(string) Backend.
func RegisterBackendFactory(name string, factory any) {
	switch factory.(type) {
	case func() Backend, func(string) Backend:
		backendFactories.Store(name, factory)
	default:
		panic(fmt.Sprintf("chantrace.RegisterBackendFactory: unsupported factory type %T", factory))
	}
}

// Enable starts tracing with the given options.
// Calling Enable again replaces existing backends.
// If no backends are specified, defaults to WithLogStream().
func Enable(opts ...Option) {
	shutdownMu.Lock()
	defer shutdownMu.Unlock()

	cfg := &traceConfig{}
	for _, opt := range opts {
		opt(cfg)
	}
	if len(cfg.backends) == 0 {
		cfg.backends = append(cfg.backends, newLogStream())
	}
	if cfg.bufSize > 0 {
		defaultCollector.bufSize = cfg.bufSize
	}
	if cfg.snapValues != nil {
		snapshotValues.Store(*cfg.snapValues)
	} else {
		snapshotValues.Store(true) // default: capture values
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
}

// Shutdown stops tracing and flushes all backends.
func Shutdown() {
	shutdownMu.Lock()
	defer shutdownMu.Unlock()

	enabled.Store(false)
	defaultCollector.closeBackends()
}

// Enabled reports whether tracing is currently active.
func Enabled() bool {
	return enabled.Load()
}
