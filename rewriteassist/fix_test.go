package rewriteassist

import (
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"go/types"
	"strings"
	"testing"
)

func TestRewriteFile(t *testing.T) {
	const src = `package p

func f(ch chan int, ro <-chan int) {
	ch <- 1
	v := <-ro
	_, ok := <-ro
	for x := range ro {
		_ = x
	}
	_, _ = v, ok
}
`

	out, stats := rewriteSource(t, src)
	if stats.Send != 1 || stats.Recv != 1 || stats.RecvOK != 1 || stats.Range != 1 {
		t.Fatalf("stats = %+v, want send=1 recv=1 recvOK=1 range=1", stats)
	}

	assertContains(t, out, `"github.com/khzaw/chantrace"`)
	assertContains(t, out, `chantrace.Send(ch, 1)`)
	assertContains(t, out, `v := chantrace.Recv(ro)`)
	assertContains(t, out, `_, ok := chantrace.RecvOk(ro)`)
	assertContains(t, out, `for x := range chantrace.Range(ro)`)
}

func TestRewriteFileUsesExistingAliasImport(t *testing.T) {
	const src = `package p

import ct "github.com/khzaw/chantrace"

func f(ro <-chan int) {
	_ = <-ro
}
`

	out, stats := rewriteSource(t, src)
	if stats.Recv != 1 {
		t.Fatalf("stats.Recv = %d, want 1", stats.Recv)
	}
	assertContains(t, out, `ct.Recv(ro)`)
	assertContains(t, out, `import ct "github.com/khzaw/chantrace"`)
}

func TestRewriteFileNilInput(t *testing.T) {
	stats := RewriteFile(nil, nil, nil)
	if stats.Total() != 0 {
		t.Fatalf("stats.Total = %d, want 0", stats.Total())
	}
}

func TestRewriteFileNoChange(t *testing.T) {
	const src = `package p

func f(x int) int {
	return x + 1
}
`

	out, stats := rewriteSource(t, src)
	if stats.Total() != 0 {
		t.Fatalf("stats.Total = %d, want 0", stats.Total())
	}
	assertNotContains(t, out, `"github.com/khzaw/chantrace"`)
}

func rewriteSource(t *testing.T, src string) (string, RewriteStats) {
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
		Importer: testImporter{},
	}
	if _, err := conf.Check("sample", fset, []*ast.File{file}, info); err != nil {
		t.Fatalf("type-check: %v", err)
	}

	stats := RewriteFile(fset, file, info)
	var b strings.Builder
	if err := format.Node(&b, fset, file); err != nil {
		t.Fatalf("format.Node: %v", err)
	}
	return b.String(), stats
}

type testImporter struct{}

func (testImporter) Import(path string) (*types.Package, error) {
	if path == chantraceImportPath {
		return types.NewPackage(path, "chantrace"), nil
	}
	return nil, fmt.Errorf("import not supported in test: %s", path)
}

func assertContains(t *testing.T, s, want string) {
	t.Helper()
	if !strings.Contains(s, want) {
		t.Fatalf("output missing %q\noutput:\n%s", want, s)
	}
}

func assertNotContains(t *testing.T, s, want string) {
	t.Helper()
	if strings.Contains(s, want) {
		t.Fatalf("output unexpectedly contains %q\noutput:\n%s", want, s)
	}
}
