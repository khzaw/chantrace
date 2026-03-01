package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunDryRunReportsRewrites(t *testing.T) {
	dir := setupFixture(t)
	t.Chdir(dir)

	var out bytes.Buffer
	var errOut bytes.Buffer
	code := run([]string{"./..."}, &out, &errOut)
	if code != 0 {
		t.Fatalf("run() code = %d, stderr=%s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "found 1 file(s), 4 site(s) to rewrite") {
		t.Fatalf("stdout missing summary, got:\n%s", out.String())
	}

	src, err := os.ReadFile(filepath.Join(dir, "main.go"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if strings.Contains(string(src), "chantrace.Send") {
		t.Fatal("source unexpectedly rewritten without -w")
	}
}

func TestRunWriteModeAppliesRewrites(t *testing.T) {
	dir := setupFixture(t)
	t.Chdir(dir)

	var out bytes.Buffer
	var errOut bytes.Buffer
	code := run([]string{"-w", "./..."}, &out, &errOut)
	if code != 0 {
		t.Fatalf("run() code = %d, stderr=%s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "rewrote 1 file(s), 4 site(s)") {
		t.Fatalf("stdout missing rewrite summary, got:\n%s", out.String())
	}

	src, err := os.ReadFile(filepath.Join(dir, "main.go"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	text := string(src)
	if !strings.Contains(text, `"github.com/khzaw/chantrace"`) {
		t.Fatal("missing chantrace import after rewrite")
	}
	if !strings.Contains(text, "chantrace.Send(ch, 1)") {
		t.Fatal("missing send rewrite")
	}
	if !strings.Contains(text, "v := chantrace.Recv(ro)") {
		t.Fatal("missing recv rewrite")
	}
	if !strings.Contains(text, "_, ok := chantrace.RecvOk(ro)") {
		t.Fatal("missing recv-ok rewrite")
	}
	if !strings.Contains(text, "for range chantrace.Range(ro)") {
		t.Fatal("missing range rewrite")
	}
}

func setupFixture(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/app\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatalf("WriteFile go.mod: %v", err)
	}

	const src = `package main

func f(ch chan int, ro <-chan int) {
	ch <- 1
	v := <-ro
	_, ok := <-ro
	for range ro {
	}
	_, _ = v, ok
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatalf("WriteFile main.go: %v", err)
	}
	return dir
}
