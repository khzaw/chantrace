package rewriteassist

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"strings"
	"testing"
)

type stubImporter struct {
	base types.Importer
}

func (s stubImporter) Import(path string) (*types.Package, error) {
	if path == chantraceImportPath {
		return types.NewPackage(path, "chantrace"), nil
	}
	return s.base.Import(path)
}

func mustParseAndTypecheck(t *testing.T, src string) (*token.FileSet, *ast.File, *types.Info) {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "sample.go", src, parser.ParseComments)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	info := &types.Info{
		Types: make(map[ast.Expr]types.TypeAndValue),
		Defs:  make(map[*ast.Ident]types.Object),
		Uses:  make(map[*ast.Ident]types.Object),
	}
	conf := &types.Config{
		Importer: stubImporter{base: importer.Default()},
	}
	if _, err := conf.Check("sample", fset, []*ast.File{file}, info); err != nil {
		t.Fatalf("type-check: %v", err)
	}
	return fset, file, info
}

func mustFormat(t *testing.T, fset *token.FileSet, file *ast.File) string {
	t.Helper()
	var b bytes.Buffer
	if err := format.Node(&b, fset, file); err != nil {
		t.Fatalf("format.Node: %v", err)
	}
	return b.String()
}

func TestRewriteFileSendRecvRange(t *testing.T) {
	const src = `package p
func f(ch chan int, ro <-chan int) {
	ch <- 1
	x := <-ro
	_ = <-ro
	for v := range ro {
		_ = v
	}
	_ = x
}
`

	fset, file, info := mustParseAndTypecheck(t, src)
	got := RewriteFile(fset, file, info, DefaultRewriteConfig())
	if !got.Changed {
		t.Fatal("Changed = false, want true")
	}
	if got.Rewrites < 4 {
		t.Fatalf("Rewrites = %d, want >= 4", got.Rewrites)
	}
	out := mustFormat(t, fset, file)
	wantSubstrings := []string{
		`import "github.com/khzaw/chantrace"`,
		`chantrace.Send(ch, 1)`,
		`x := chantrace.Recv(ro)`,
		`_ = chantrace.Recv(ro)`,
		`for v := range chantrace.Range(ro) {`,
	}
	for _, sub := range wantSubstrings {
		if !strings.Contains(out, sub) {
			t.Fatalf("output missing %q:\n%s", sub, out)
		}
	}
}

func TestRewriteFileRecvOk(t *testing.T) {
	const src = `package p
func f(ro <-chan int) {
	v, ok := <-ro
	_, _ = v, ok
}
`

	fset, file, info := mustParseAndTypecheck(t, src)
	got := RewriteFile(fset, file, info, DefaultRewriteConfig())
	if !got.Changed {
		t.Fatal("Changed = false, want true")
	}
	out := mustFormat(t, fset, file)
	if !strings.Contains(out, `v, ok := chantrace.RecvOk(ro)`) {
		t.Fatalf("missing RecvOk rewrite:\n%s", out)
	}
}

func TestRewriteFileGoStmtIssue(t *testing.T) {
	const src = `package p
func f() {
	go func() {}()
}
`

	fset, file, info := mustParseAndTypecheck(t, src)
	got := RewriteFile(fset, file, info, DefaultRewriteConfig())
	if got.Changed {
		t.Fatal("Changed = true, want false")
	}
	if len(got.Issues) != 1 {
		t.Fatalf("issue count = %d, want 1", len(got.Issues))
	}
	if !strings.Contains(got.Issues[0].Message, "manual migration") {
		t.Fatalf("unexpected issue message: %q", got.Issues[0].Message)
	}
}

func TestRewriteFileRespectsImportAlias(t *testing.T) {
	const src = `package p
import ct "github.com/khzaw/chantrace"
func f(ch chan int) {
	ch <- 1
}
`

	fset, file, info := mustParseAndTypecheck(t, src)
	got := RewriteFile(fset, file, info, DefaultRewriteConfig())
	if !got.Changed {
		t.Fatal("Changed = false, want true")
	}
	out := mustFormat(t, fset, file)
	if !strings.Contains(out, `ct.Send(ch, 1)`) {
		t.Fatalf("missing aliased Send rewrite:\n%s", out)
	}
}

func TestRewriteFileSkipsUnsupportedImportAlias(t *testing.T) {
	const src = `package p
import _ "github.com/khzaw/chantrace"
func f(ch chan int) {
	ch <- 1
}
`

	fset, file, info := mustParseAndTypecheck(t, src)
	got := RewriteFile(fset, file, info, DefaultRewriteConfig())
	if got.Changed {
		t.Fatal("Changed = true, want false")
	}
	if len(got.Issues) == 0 {
		t.Fatal("expected issue for unsupported import alias")
	}
}
