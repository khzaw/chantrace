package chantrace

import "testing"

func BenchmarkNextGoroutineID(b *testing.B) {
	for b.Loop() {
		nextGoroutineID()
	}
}

func BenchmarkCapturePC(b *testing.B) {
	for b.Loop() {
		capturePC(3)
	}
}

func BenchmarkResolvePC(b *testing.B) {
	pc := capturePC(3)
	b.ResetTimer()
	for b.Loop() {
		resolvePC(pc)
	}
}

func BenchmarkCurrentRuntimeGID(b *testing.B) {
	for b.Loop() {
		currentRuntimeGID()
	}
}
