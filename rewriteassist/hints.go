package rewriteassist

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"sort"
)

// HintKind categorizes migration hints for chantrace adoption.
type HintKind string

const (
	HintSend    HintKind = "send"
	HintRecv    HintKind = "recv"
	HintRange   HintKind = "range"
	HintGoSpawn HintKind = "go"
)

// Hint describes a location that likely requires chantrace wrapping.
type Hint struct {
	Kind       HintKind
	Pos        token.Pos
	Position   token.Position
	Message    string
	Suggestion string
}

// CollectFile inspects one file and returns migration hints for native
// concurrency constructs that bypass chantrace wrappers.
func CollectFile(fset *token.FileSet, file *ast.File, info *types.Info) []Hint {
	if file == nil || info == nil {
		return nil
	}

	var hints []Hint
	ast.Inspect(file, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.SendStmt:
			if isChanType(info.TypeOf(n.Chan)) {
				hints = append(hints, Hint{
					Kind:       HintSend,
					Pos:        n.Pos(),
					Position:   fset.Position(n.Pos()),
					Message:    "direct channel send is not traced",
					Suggestion: "replace with chantrace.Send(ch, value)",
				})
			}
		case *ast.UnaryExpr:
			if n.Op == token.ARROW && isChanType(info.TypeOf(n.X)) {
				hints = append(hints, Hint{
					Kind:       HintRecv,
					Pos:        n.Pos(),
					Position:   fset.Position(n.Pos()),
					Message:    "direct channel receive is not traced",
					Suggestion: "replace with chantrace.Recv(ch) or chantrace.RecvOk(ch)",
				})
			}
		case *ast.RangeStmt:
			if isChanType(info.TypeOf(n.X)) {
				hints = append(hints, Hint{
					Kind:       HintRange,
					Pos:        n.Pos(),
					Position:   fset.Position(n.Pos()),
					Message:    "range over channel is not traced",
					Suggestion: "replace with for v := range chantrace.Range(ch) { ... }",
				})
			}
		case *ast.GoStmt:
			hints = append(hints, Hint{
				Kind:       HintGoSpawn,
				Pos:        n.Pos(),
				Position:   fset.Position(n.Pos()),
				Message:    "goroutine launched with go is not traced",
				Suggestion: "replace with chantrace.Go(ctx, label, func(ctx context.Context) { ... })",
			})
		}
		return true
	})

	sort.Slice(hints, func(i, j int) bool { return hints[i].Pos < hints[j].Pos })
	return hints
}

func isChanType(t types.Type) bool {
	if t == nil {
		return false
	}
	_, ok := t.Underlying().(*types.Chan)
	return ok
}

func (h Hint) String() string {
	return fmt.Sprintf("%s:%d:%d: %s: %s",
		h.Position.Filename,
		h.Position.Line,
		h.Position.Column,
		h.Kind,
		h.Message,
	)
}
