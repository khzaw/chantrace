package chantrace

import "context"

func Send[T any](chan<- T, T) {}

func Recv[T any](<-chan T) T {
	var zero T
	return zero
}

func RecvOk[T any](<-chan T) (T, bool) {
	var zero T
	return zero, false
}

func Range[T any](<-chan T) []T {
	return nil
}

func Go(context.Context, string, func(context.Context)) {}
