// Package chantrace is a low-perturbation runtime probe for diagnosing Go
// concurrency problems — goroutine leaks, deadlocks, and contention — without
// touching application code.
//
// It works by passively polling [runtime.NumGoroutine] on a fixed interval,
// establishing a baseline goroutine count, and opening short
// [runtime.SetBlockProfileRate] / [runtime.SetMutexProfileFraction] capture
// windows when the live count rises above baseline by a configurable delta for
// a configurable number of consecutive samples. The most recent block and mutex
// profile summaries are retained and exposed as a snapshot.
//
// No instrumentation of channel operations is required.
//
// # Quickstart
//
// Passive, with defaults:
//
//	chantrace.Enable(chantrace.WithNoTouch())
//	defer chantrace.Shutdown()
//
// Read the current state whenever you like:
//
//	report := chantrace.NoTouchReport()
//	// report.Mode, report.Baseline, report.CurrentDelta, report.TriggerCount,
//	// report.LastBlockProfile, report.LastMutexProfile, report.Samples, ...
//
// Or enable with no code change via the environment:
//
//	CHANTRACE=notouch ./yourserver
//
// which is equivalent to Enable(WithNoTouch()) with the defaults below.
//
// # Defaults
//
//	PollInterval        250ms    sampling cadence
//	HistorySize         256      samples retained in the snapshot
//	BaselineSamples     12       samples averaged to form the baseline
//	TriggerDelta        32       goroutines above baseline to count as anomalous
//	TriggerConsecutive  3        anomalous samples in a row before a window opens
//	TriggerWindow       3s       how long block/mutex profiling stays on per trigger
//	Cooldown            5s       delay before another window can open
//	BlockProfileRate    1000000  passed to runtime.SetBlockProfileRate during a window
//	MutexProfileFraction 8       passed to runtime.SetMutexProfileFraction during a window
//
// Tune any of them via the WithNoTouch* options.
//
// # HTTP debug endpoint
//
// Blank-import the debug subpackage to expose the snapshot over HTTP, like
// [net/http/pprof]:
//
//	import _ "github.com/khzaw/chantrace/debug"
//
//	GET /debug/chantrace/notouch  — snapshot as JSON
//
// # Note
//
// An earlier version of this package also provided an instrumented channel
// wrapper API (Send/Recv/Select/Go/Make) plus migration tooling. That API has
// been removed; the no-touch probe is the sole focus. See the README for the
// rationale.
package chantrace
