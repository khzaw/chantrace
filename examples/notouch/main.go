// Command notouch demonstrates the chantrace no-touch runtime probe: enable it,
// run a workload that briefly spikes the goroutine count, then print the probe
// snapshot (baseline, delta, trigger count, captured profiles) as JSON.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/khzaw/chantrace"
)

func main() {
	chantrace.Enable(
		chantrace.WithNoTouch(
			// A short poll interval so the demo produces samples quickly. In real
			// use, leave this at the default and let the probe run quietly.
			chantrace.WithNoTouchPollInterval(20*time.Millisecond),
			// Baseline off the first sample, then fire a profile window on any
			// goroutine-count increase above 5.
			chantrace.WithNoTouchBaselineSamples(1),
			chantrace.WithNoTouchTriggerDelta(5),
			chantrace.WithNoTouchTriggerConsecutive(1),
			chantrace.WithNoTouchTriggerWindow(100*time.Millisecond),
			chantrace.WithNoTouchCooldown(50*time.Millisecond),
			chantrace.WithNoTouchBlockProfileRate(1),
			chantrace.WithNoTouchMutexProfileFraction(1),
		),
	)
	defer chantrace.Shutdown()

	// Let the poller take its baseline sample first, while the goroutine count
	// is at rest, so the spike below is measured relative to a calm baseline.
	time.Sleep(60 * time.Millisecond)

	// Spawn a burst of goroutines that block briefly, raising the live count
	// above the baseline and tripping a trigger window.
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			time.Sleep(300 * time.Millisecond)
		}()
	}

	// Give the poller time to observe the spike, open a window, and capture.
	time.Sleep(400 * time.Millisecond)
	wg.Wait()

	report := chantrace.NoTouchReport()
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(report); err != nil {
		fmt.Fprintln(os.Stderr, "encode:", err)
		os.Exit(1)
	}
}
