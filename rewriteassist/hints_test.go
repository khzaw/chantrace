package rewriteassist

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"testing"
)

func TestCollectFile(t *testing.T) {
	const src = `package p
func f(ch chan int, ro <-chan int) {
	ch <- 1
	_ = <-ro
	for range ro {
	}
	go func() {}()
}
`

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "sample.go", src, 0)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}

	info := &types.Info{
		Types: make(map[ast.Expr]types.TypeAndValue),
		Defs:  make(map[*ast.Ident]types.Object),
		Uses:  make(map[*ast.Ident]types.Object),
	}
	conf := &types.Config{}
	if _, err := conf.Check("sample", fset, []*ast.File{file}, info); err != nil {
		t.Fatalf("type-check: %v", err)
	}

	hints := CollectFile(fset, file, info)
	if len(hints) != 4 {
		t.Fatalf("hint count = %d, want 4", len(hints))
	}

	want := []HintKind{HintSend, HintRecv, HintRange, HintGoSpawn}
	for i, k := range want {
		if hints[i].Kind != k {
			t.Fatalf("hint[%d].Kind = %q, want %q", i, hints[i].Kind, k)
		}
	}
}

func TestCollectFileNilInput(t *testing.T) {
	if got := CollectFile(nil, nil, nil); len(got) != 0 {
		t.Fatalf("CollectFile(nil,nil,nil) len = %d, want 0", len(got))
	}
}
