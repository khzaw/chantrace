package chantrace

import (
	"os"
	"strings"
	"sync"
	"sync/atomic"
)

// enabled records whether the no-touch probe (if configured) is currently
// running. It is read by Enabled and written under shutdownMu by Enable and
// Shutdown.
var enabled atomic.Bool

// shutdownMu serializes Enable/Shutdown so that start/stop of the probe
// cannot race with itself.
var shutdownMu sync.Mutex

// pcCapture / pcSampleEvery / snapshotValues existed for the removed wrapper
// API. They are intentionally gone: the only flagship is the no-touch runtime
// probe, which does not capture program counters or channel value snapshots.

func init() {
	autoEnable(strings.ToLower(strings.TrimSpace(os.Getenv("CHANTRACE"))))
}

// autoEnable wires the CHANTRACE environment variable to a no-op unless it is
// explicitly "notouch". Any other value (including the empty string) leaves the
// probe disabled; users must call Enable(WithNoTouch(...)) themselves.
func autoEnable(v string) {
	switch v {
	case "notouch":
		Enable(WithNoTouch())
	default:
		// Unrecognized or unset: leave it to the caller.
	}
}

// traceConfig is the resolved configuration consumed by Enable. Only the
// no-touch probe remains; the backend/collector/PC fields are gone.
type traceConfig struct {
	noTouch *NoTouchConfig
}

// Option configures the tracing session.
type Option func(*traceConfig)

// WithNoTouch enables the no-touch runtime probe: low-perturbation goroutine-
// count sampling with anomaly-triggered block/mutex profile capture windows.
// It requires no instrumentation of channel operations and is the recommended
// way to use chantrace.
//
// With no options the defaults from defaultNoTouchConfig are used; pass
// WithNoTouch* tunables to adjust them.
func WithNoTouch(opts ...NoTouchOption) Option {
	return func(c *traceConfig) {
		cfg := defaultNoTouchConfig()
		applyNoTouchEnv(&cfg, os.Getenv)
		for _, opt := range opts {
			opt(&cfg)
		}
		c.noTouch = &cfg
	}
}

// Enable starts a tracing session. With no options the session is inert; pass
// WithNoTouch (and its WithNoTouch* tunables) to activate the runtime probe.
//
// Calling Enable again (with or without WithNoTouch) first stops any probe that
// is currently running, so re-enabling without WithNoTouch disables the probe.
//
// CHANTRACE=notouch in the environment enables the probe with defaults without
// any code change.
func Enable(opts ...Option) {
	cfg := traceConfig{}
	for _, opt := range opts {
		opt(&cfg)
	}

	shutdownMu.Lock()
	defer shutdownMu.Unlock()

	stopNoTouchLocked()

	if cfg.noTouch != nil {
		startNoTouchLocked(*cfg.noTouch)
	}
	enabled.Store(cfg.noTouch != nil)
}

// Shutdown stops any active probe and restores runtime profile rates the probe
// may have changed. It is safe to call multiple times.
func Shutdown() {
	shutdownMu.Lock()
	defer shutdownMu.Unlock()
	stopNoTouchLocked()
	enabled.Store(false)
}

// Enabled reports whether the no-touch probe is currently running.
func Enabled() bool { return enabled.Load() }
