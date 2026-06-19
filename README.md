# chantrace

A low-perturbation runtime probe for diagnosing Go concurrency problems —
goroutine leaks, deadlocks, and contention — **without touching application
code.**

chantrace watches the live goroutine count, learns a baseline, and opens short
block/mutex profile capture windows when the count spikes. No wrapping of
channel operations, no code changes to call sites, no static analysis pass. Flip
it on with one line (or one environment variable), let it run in production, and
read a snapshot when something goes wrong.

> **Status / history.** An earlier version of this package shipped an
> instrumented channel-wrapper API (`Send`/`Recv`/`Select`/`Go`/`Make`) plus a
> migration ecosystem (codemod, static analyzer, backends). That surface has
> been removed; the no-touch probe below is the sole focus. Rationale is at the
> end of this document.

---

## How it works

1. **Passive polling.** Every `PollInterval` (default 250ms), chantrace samples
   `runtime.NumGoroutine()` and keeps the last `HistorySize` (default 256).
2. **Baselining.** The first `BaselineSamples` samples (default 12) are averaged
   to form a baseline goroutine count. Until the baseline is ready the probe
   reports `mode: "warming-up"`; after, `mode: "passive"`.
3. **Anomaly-triggered capture.** When the live count exceeds the baseline by
   `TriggerDelta` (default 32) goroutines for `TriggerConsecutive` (default 3)
   samples in a row, the probe opens a `TriggerWindow` (default 3s) during which
   it calls `runtime.SetBlockProfileRate` / `runtime.SetMutexProfileFraction`.
4. **Profile retention.** At the end of each window it captures a text summary
   of the block and mutex profiles and restores the previous runtime rates.
5. **Cooldown.** A `Cooldown` (default 5s) delay prevents windows from firing
   back-to-back on a sustained spike.

The result is surfaced as a single snapshot, `NoTouchReport()`.

## Quickstart

```go
package main

import "github.com/khzaw/chantrace"

func main() {
    chantrace.Enable(chantrace.WithNoTouch())
    defer chantrace.Shutdown()

    // ... your program ...

    report := chantrace.NoTouchReport()
    // report.Mode, report.Baseline, report.CurrentDelta,
    // report.TriggerCount, report.LastBlockProfile,
    // report.LastMutexProfile, report.Samples, ...
}
```

Or, with **no code change at all**, via the environment:

```
CHANTRACE=notouch ./yourserver
```

which is equivalent to `Enable(WithNoTouch())` with the defaults.

### Tuning

Every default is adjustable via the `WithNoTouch*` options — sampling cadence,
history size, baseline warmup, trigger delta/consecutive, window and cooldown
durations, and the profile rates and summary size limits. See the package
documentation (`go doc github.com/khzaw/chantrace`) for the full list.

Operators can also tune the probe at runtime via environment variables (no code
change or rebuild required), alongside `CHANTRACE=notouch`:

```bash
export CHANTRACE=notouch
export CHANTRACE_NOTOUCH_POLL_MS=250
export CHANTRACE_NOTOUCH_TRIGGER_DELTA=32
export CHANTRACE_NOTOUCH_TRIGGER_CONSECUTIVE=3
export CHANTRACE_NOTOUCH_TRIGGER_WINDOW_MS=3000
export CHANTRACE_NOTOUCH_COOLDOWN_MS=5000
```

## HTTP debug endpoint

Blank-import the `debug` subpackage to expose the snapshot over HTTP, like
`net/http/pprof`:

```go
import _ "github.com/khzaw/chantrace/debug"
```

```
GET /debug/chantrace/         — index page
GET /debug/chantrace/notouch  — probe snapshot (JSON)
```

```
GET /debug/chantrace/         — index page (live polling dashboard)
GET /debug/chantrace/notouch  — full no-touch probe snapshot (JSON)
GET /debug/chantrace/report   — compact no-touch incident report (JSON)
```

Point a browser at `/debug/chantrace/` in any server that already serves the
default mux.

## Why this instead of `net/http/pprof`?

chantrace is **not** a replacement for pprof — it's a thin layer on top of it.
The difference is *when* profiling is on:

- `net/http/pprof` profiles on demand, when you fetch `/debug/pprof/block`.
  For intermittent problems (a leak that only manifests under specific load, a
  deadlock that clears before you notice) you have to be watching at the right
  moment.
- chantrace keeps profiling **off** by default (so there's essentially no
  steady-state cost) and opens short capture windows automatically when the
  goroutine count behaves anomalously — baselining, anomaly detection, trigger
  windows, and cooldown are the value on top.

Think of it as a tripwire that records a pprof snapshot of the moment things
went wrong, even if nobody was looking.

## Example

See [`examples/notouch`](examples/notouch): it enables the probe, spawns a burst
of goroutines to trip a trigger window, and prints the resulting snapshot as
JSON.

```bash
go run ./examples/notouch
```

## Defaults

| Setting | Default | Meaning |
| --- | --- | --- |
| `PollInterval` | 250ms | sampling cadence |
| `HistorySize` | 256 | samples retained in the snapshot |
| `BaselineSamples` | 12 | samples averaged to form the baseline |
| `TriggerDelta` | 32 | goroutines above baseline to count as anomalous |
| `TriggerConsecutive` | 3 | anomalous samples in a row before a window opens |
| `TriggerWindow` | 3s | how long block/mutex profiling stays on per trigger |
| `Cooldown` | 5s | delay before another window can open |
| `BlockProfileRate` | 1000000 | `runtime.SetBlockProfileRate` during a window |
| `MutexProfileFraction` | 8 | `runtime.SetMutexProfileFraction` during a window |

## Rationale for the API change

The removed wrapper API required replacing every native channel operation
(`ch <- v`, `<-ch`, `select { case ... }`, `go func()`) with a chantrace
equivalent. In practice that didn't hold up:

- **Coverage was all-or-nothing.** A single unwrapped op was a blind spot that
  defeated the Start/Done model the analyzer depended on, so the payoff required
  near-100% instrumentation — a large, permanent, review-heavy rewrite of every
  concurrent package.
- **`select` was the dealbreaker.** The wrapper's `select` was reflect-backed
  and callback-shaped, nothing like `select { case }`, and couldn't be codemod'd.
  Idiomatic Go leans heavily on `case <-ctx.Done()`, which made adoption cost
  highest exactly where it mattered most.
- **The no-touch probe delivered most of the diagnostic value with none of the
  cost.** Once it existed, the invasive path stopped justifying itself.

So the invasive API and its migration tooling were removed, and development
focus narrowed to making the no-touch probe the best possible tripwire.
