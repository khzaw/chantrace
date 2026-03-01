package chantrace_test

import (
	_ "github.com/khzaw/chantrace/backend/tui"
	_ "github.com/khzaw/chantrace/backend/web"
	"testing"

	"github.com/khzaw/chantrace"
)

func TestWithTUIImportedBackendSmoke(t *testing.T) {
	chantrace.Enable(chantrace.WithTUI())
	t.Cleanup(chantrace.Shutdown)

	ch := chantrace.Make[int]("tui-smoke", 1)
	chantrace.Send(ch, 1)
	if got := chantrace.Recv[int](ch); got != 1 {
		t.Fatalf("Recv = %d, want 1", got)
	}
}

func TestWithWebImportedBackendSmoke(t *testing.T) {
	chantrace.Enable(chantrace.WithWeb("127.0.0.1:0"))
	t.Cleanup(chantrace.Shutdown)

	ch := chantrace.Make[int]("web-smoke", 1)
	chantrace.Send(ch, 2)
	if got := chantrace.Recv[int](ch); got != 2 {
		t.Fatalf("Recv = %d, want 2", got)
	}
}
