// Package chantrace provides drop-in tracing for Go channels.
//
// Channels remain plain chan T values. Traced operations are free functions
// that check a single atomic bool (~1ns overhead when disabled), then perform
// the native channel operation. Enable tracing to see what's flowing.
//
//	orders := chantrace.Make[Order]("orders", 10) // traced chan Order
//	chantrace.Send(orders, Order{ID: 1})          // traced send
//	order := chantrace.Recv(orders)                // traced receive
//	orders <- Order{ID: 2}                         // still works, just untraced
//
// Tracing can be enabled via environment variable or programmatically:
//
//	CHANTRACE=1 go run .                           // log to stderr
//	CHANTRACE=notouch go run .                     // no-touch runtime probe
//	chantrace.Enable(chantrace.WithLogStream())    // programmatic
//	defer chantrace.Shutdown()
package chantrace
