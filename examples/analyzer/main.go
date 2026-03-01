package main

import (
	"context"
	"fmt"
	"time"

	"github.com/khzaw/chantrace"
)

func main() {
	analyzer := chantrace.NewAnalyzer(
		chantrace.WithAnalyzerBlockedThreshold(10*time.Millisecond),
		chantrace.WithAnalyzerLeakThreshold(20*time.Millisecond),
	)

	chantrace.Enable(
		chantrace.WithLogStream(),
		chantrace.WithBackend(analyzer),
		chantrace.WithPCCapture(false),
	)
	defer chantrace.Shutdown()

	ctx := context.Background()
	blocked := chantrace.Make[int]("blocked-send")
	release := chantrace.Make[struct{}]("release")

	chantrace.Go(ctx, "blocked-sender", func(context.Context) {
		chantrace.Send(blocked, 1)
	})

	chantrace.Go(ctx, "long-worker", func(context.Context) {
		<-release
	})

	time.Sleep(30 * time.Millisecond)
	r := analyzer.Report()
	fmt.Printf(
		"blocked=%d leaked=%d deadlocks=%d channel_waits=%d graph(nodes=%d, edges=%d) uncertain=%v\n",
		len(r.Blocked),
		len(r.Leaked),
		len(r.Deadlocks),
		len(r.ChannelWaits),
		len(r.WaitGraph.Nodes),
		len(r.WaitGraph.Edges),
		r.StateUncertain,
	)

	_ = chantrace.Recv[int](blocked)
	chantrace.Close(release)

	time.Sleep(20 * time.Millisecond)
	r = analyzer.Report()
	fmt.Printf(
		"after cleanup: blocked=%d leaked=%d deadlocks=%d channel_waits=%d graph(nodes=%d, edges=%d) uncertain=%v\n",
		len(r.Blocked),
		len(r.Leaked),
		len(r.Deadlocks),
		len(r.ChannelWaits),
		len(r.WaitGraph.Nodes),
		len(r.WaitGraph.Edges),
		r.StateUncertain,
	)
}
