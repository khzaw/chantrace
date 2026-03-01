package chantracecheck

import (
	"go/ast"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
)

// Analyzer reports native channel/goroutine operations that bypass chantrace.
var Analyzer = &analysis.Analyzer{
	Name:     "chantracecheck",
	Doc:      "report native channel ops/go statements that are not chantrace-wrapped",
	Requires: []*analysis.Analyzer{inspect.Analyzer},
	Run:      run,
}

func run(pass *analysis.Pass) (any, error) {
	ins := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)

	nodeFilter := []ast.Node{
		(*ast.SendStmt)(nil),
		(*ast.GoStmt)(nil),
		(*ast.RangeStmt)(nil),
		(*ast.UnaryExpr)(nil),
	}

	ins.Preorder(nodeFilter, func(node ast.Node) {
		switch n := node.(type) {
		case *ast.SendStmt:
			if isChanType(pass.TypesInfo.TypeOf(n.Chan)) {
				pass.Reportf(n.Pos(), "direct channel send is not traced; use chantrace.Send")
			}
		case *ast.GoStmt:
			pass.Reportf(n.Pos(), "goroutine launched with go is not traced; use chantrace.Go with context")
		case *ast.RangeStmt:
			if isChanType(pass.TypesInfo.TypeOf(n.X)) {
				pass.Reportf(n.Pos(), "range over channel is not traced; use chantrace.Range")
			}
		case *ast.UnaryExpr:
			if n.Op != token.ARROW {
				return
			}
			if isChanType(pass.TypesInfo.TypeOf(n.X)) {
				pass.Reportf(n.Pos(), "direct channel receive is not traced; use chantrace.Recv or chantrace.RecvOk")
			}
		}
	})

	return nil, nil
}

func isChanType(t types.Type) bool {
	if t == nil {
		return false
	}
	_, ok := t.Underlying().(*types.Chan)
	return ok
}
