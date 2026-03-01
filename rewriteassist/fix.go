package rewriteassist

import (
	"go/ast"
	"go/token"
	"go/types"
	"strconv"

	"golang.org/x/tools/go/ast/astutil"
)

const chantraceImportPath = "github.com/khzaw/chantrace"

// RewriteStats reports how many rewrites were applied to one file.
type RewriteStats struct {
	Send   int
	Recv   int
	RecvOK int
	Range  int
}

// Total is the total number of rewritten sites.
func (s RewriteStats) Total() int {
	return s.Send + s.Recv + s.RecvOK + s.Range
}

// RewriteFile rewrites native channel operations in one file into chantrace wrappers.
// It returns rewrite counts for each operation kind.
func RewriteFile(fset *token.FileSet, file *ast.File, info *types.Info) RewriteStats {
	if fset == nil || file == nil || info == nil {
		return RewriteStats{}
	}

	qualifier, imported := chantraceQualifier(file)
	if qualifier == "" {
		qualifier = chooseQualifier(file)
	}

	var stats RewriteStats
	astutil.Apply(file, func(c *astutil.Cursor) bool {
		switch n := c.Node().(type) {
		case *ast.AssignStmt:
			rewriteRecvOKAssign(n, info, qualifier, &stats)
		case *ast.ValueSpec:
			rewriteRecvOKValueSpec(n, info, qualifier, &stats)
		case *ast.SendStmt:
			if !isChanType(info.TypeOf(n.Chan)) {
				return true
			}
			c.Replace(&ast.ExprStmt{
				X: callSelector(qualifier, "Send", n.Chan, n.Value),
			})
			stats.Send++
			return false
		case *ast.UnaryExpr:
			if n.Op != token.ARROW || !isChanType(info.TypeOf(n.X)) {
				return true
			}
			c.Replace(callSelector(qualifier, "Recv", n.X))
			stats.Recv++
			return false
		case *ast.RangeStmt:
			if !isChanType(info.TypeOf(n.X)) {
				return true
			}
			n.X = callSelector(qualifier, "Range", n.X)
			stats.Range++
		}
		return true
	}, nil)

	if stats.Total() == 0 || imported {
		return stats
	}

	if qualifier == "chantrace" {
		astutil.AddImport(fset, file, chantraceImportPath)
		return stats
	}

	astutil.AddNamedImport(fset, file, qualifier, chantraceImportPath)
	return stats
}

func rewriteRecvOKAssign(n *ast.AssignStmt, info *types.Info, qualifier string, stats *RewriteStats) {
	if len(n.Lhs) != 2 || len(n.Rhs) != 1 {
		return
	}
	recv, ok := n.Rhs[0].(*ast.UnaryExpr)
	if !ok || recv.Op != token.ARROW || !isChanType(info.TypeOf(recv.X)) {
		return
	}
	n.Rhs[0] = callSelector(qualifier, "RecvOk", recv.X)
	stats.RecvOK++
}

func rewriteRecvOKValueSpec(n *ast.ValueSpec, info *types.Info, qualifier string, stats *RewriteStats) {
	if len(n.Names) != 2 || len(n.Values) != 1 {
		return
	}
	recv, ok := n.Values[0].(*ast.UnaryExpr)
	if !ok || recv.Op != token.ARROW || !isChanType(info.TypeOf(recv.X)) {
		return
	}
	n.Values[0] = callSelector(qualifier, "RecvOk", recv.X)
	stats.RecvOK++
}

func callSelector(qualifier, name string, args ...ast.Expr) *ast.CallExpr {
	return &ast.CallExpr{
		Fun: &ast.SelectorExpr{
			X:   ast.NewIdent(qualifier),
			Sel: ast.NewIdent(name),
		},
		Args: args,
	}
}

func chantraceQualifier(file *ast.File) (qualifier string, imported bool) {
	for _, spec := range file.Imports {
		if spec.Path == nil || spec.Path.Value == "" {
			continue
		}
		if unquoteImportPath(spec.Path.Value) != chantraceImportPath {
			continue
		}

		if spec.Name == nil {
			return "chantrace", true
		}
		if spec.Name.Name == "_" || spec.Name.Name == "." {
			return "", false
		}
		return spec.Name.Name, true
	}
	return "", false
}

func chooseQualifier(file *ast.File) string {
	const base = "chantrace"
	if !identTaken(file, base) {
		return base
	}
	const fallback = "chantracepkg"
	if !identTaken(file, fallback) {
		return fallback
	}
	i := 2
	for {
		name := fallback + strconv.Itoa(i)
		if !identTaken(file, name) {
			return name
		}
		i++
	}
}

func identTaken(file *ast.File, name string) bool {
	if file.Scope != nil && file.Scope.Lookup(name) != nil {
		return true
	}
	for _, imp := range file.Imports {
		if imp.Name != nil && imp.Name.Name == name {
			return true
		}
	}
	return false
}

func unquoteImportPath(v string) string {
	if len(v) < 2 {
		return v
	}
	return v[1 : len(v)-1]
}
