//go:build !chantrace_nop

package chantrace

import "testing"

func TestDefaultBuildTracingEnabled(t *testing.T) {
	if noTracingBuild {
		t.Fatal("noTracingBuild = true in default build; want false")
	}
}
