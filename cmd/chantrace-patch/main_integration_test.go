package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func chdir(t *testing.T, dir string) {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir(%s): %v", dir, err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(prev)
	})
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", path, err)
	}
	return string(b)
}

func setupTempModule(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/tmp\n\ngo 1.24.0\n")
	src := `package tmp

func f(ch chan int, ro <-chan int) {
	ch <- 1
	_ = <-ro
}
`
	writeFile(t, filepath.Join(dir, "sample.go"), src)
	return dir
}

func TestPatchApplyStatusRevertRoundTrip(t *testing.T) {
	dir := setupTempModule(t)
	chdir(t, dir)

	orig := readFile(t, "sample.go")
	if code := runApply([]string{"./..."}); code != 0 {
		t.Fatalf("runApply code = %d, want 0", code)
	}
	if code := runStatus(nil); code != 0 {
		t.Fatalf("runStatus code = %d, want 0", code)
	}
	patched := readFile(t, "sample.go")
	if patched == orig {
		t.Fatal("sample.go unchanged after apply")
	}
	if !strings.Contains(patched, "chantrace.Send(") || !strings.Contains(patched, "chantrace.Recv(") {
		t.Fatalf("patched sample.go missing rewrites:\n%s", patched)
	}

	active, err := readActivePatchID(dir)
	if err != nil {
		t.Fatalf("readActivePatchID: %v", err)
	}
	if active == "" {
		t.Fatal("expected non-empty active patch id")
	}

	if code := runRevert(nil); code != 0 {
		t.Fatalf("runRevert code = %d, want 0", code)
	}
	if got := readFile(t, "sample.go"); got != orig {
		t.Fatalf("sample.go not restored after revert:\n%s", got)
	}
}

func TestPatchRevertRefusesDriftUnlessForced(t *testing.T) {
	dir := setupTempModule(t)
	chdir(t, dir)

	orig := readFile(t, "sample.go")
	if code := runApply([]string{"./..."}); code != 0 {
		t.Fatalf("runApply code = %d, want 0", code)
	}

	writeFile(t, "sample.go", readFile(t, "sample.go")+"\n// drift\n")

	if code := runRevert(nil); code == 0 {
		t.Fatal("runRevert without --force succeeded unexpectedly on drifted file")
	}
	if code := runRevert([]string{"--force"}); code != 0 {
		t.Fatalf("runRevert --force code = %d, want 0", code)
	}
	if got := readFile(t, "sample.go"); got != orig {
		t.Fatalf("sample.go not restored after forced revert:\n%s", got)
	}
}
