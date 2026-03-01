//go:build chantrace_nop

package chantrace

import "testing"

func TestNoopBuildTagForcesTracingOff(t *testing.T) {
	if !noTracingBuild {
		t.Fatal("noTracingBuild = false with chantrace_nop build tag; want true")
	}

	Shutdown()
	Enable(WithLogStream())
	if Enabled() {
		t.Fatal("Enabled() = true with chantrace_nop build tag; want false")
	}

	ch := make(chan int, 1)
	Send(ch, 42)
	if got := Recv(ch); got != 42 {
		t.Fatalf("Recv(ch) = %d, want 42", got)
	}
}
