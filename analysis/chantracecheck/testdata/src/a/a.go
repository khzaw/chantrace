package a

import (
	"context"

	"chantrace"
)

func directOps(ctx context.Context, ch chan int, ro <-chan int) {
	ch <- 1 // want "direct channel send is not traced"
	_ = <-ro // want "direct channel receive is not traced"

	select {
	case ch <- 2: // want "direct channel send is not traced"
	case <-ro: // want "direct channel receive is not traced"
	default:
	}

	for range ro { // want "range over channel is not traced"
	}

	go func() {}() // want "goroutine launched with go is not traced"

	chantrace.Send(ch, 3)
	_ = chantrace.Recv(ro)
	_, _ = chantrace.RecvOk(ro)

	chantrace.Go(ctx, "worker", func(context.Context) {})
	for range chantrace.Range(ro) {
	}
}
