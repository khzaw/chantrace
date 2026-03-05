package rewriteassist

import (
	"go/ast"
	"go/token"
	"go/types"
	"strconv"

	"golang.org/x/tools/go/ast/astutil"
)

const chantraceImportPath = "github.com/khzaw/chantrace"

// RewriteIssue is a non-fatal note produced while rewriting.
type RewriteIssue struct {
	Position token.Position
	Message  string
}

// RewriteConfig controls which transformations are applied.
type RewriteConfig struct {
	RewriteSend  bool
	RewriteRecv  bool
	RewriteRange bool
	ReportGoStmt bool
}

// DefaultRewriteConfig returns the default rewrite behavior.
func DefaultRewriteConfig() RewriteConfig {
	return RewriteConfig{
		RewriteSend:  true,
		RewriteRecv:  true,
		RewriteRange: true,
		ReportGoStmt: true,
	}
}

// RewriteResult summarizes transformations for one file.
type RewriteResult struct {
	Changed  bool
	Rewrites int
	Issues   []RewriteIssue
}

// RewriteFile rewrites native channel operations into chantrace wrappers.
// The input AST is mutated in place.
func RewriteFile(fset *token.FileSet, file *ast.File, info *types.Info, cfg RewriteConfig) RewriteResult {
	var out RewriteResult
	if file == nil || info == nil || fset == nil {
		return out
	}

	qual, hasImport, unusableImport := chantraceQualifier(file)
	if unusableImport {
		out.Issues = append(out.Issues, RewriteIssue{
			Position: fset.Position(file.Pos()),
			Message:  "chantrace import exists with unsupported alias (_ or .); skipping rewrites in file",
		})
		return out
	}

	astutil.Apply(file, func(c *astutil.Cursor) bool {
		node := c.Node()
		switch n := node.(type) {
		case *ast.GoStmt:
			if cfg.ReportGoStmt {
				out.Issues = append(out.Issues, RewriteIssue{
					Position: fset.Position(n.Go),
					Message:  "go statement requires manual migration to chantrace.Go",
				})
			}
		case *ast.SendStmt:
			if cfg.RewriteSend && isChanType(info.TypeOf(n.Chan)) {
				c.Replace(&ast.ExprStmt{
					X: chantraceCall(qual, "Send", n.Chan, n.Value),
				})
				out.Rewrites++
				out.Changed = true
				return false
			}
		case *ast.AssignStmt:
			if cfg.RewriteRecv && len(n.Rhs) == 1 {
				if recv, ok := n.Rhs[0].(*ast.UnaryExpr); ok && recv.Op == token.ARROW && isChanType(info.TypeOf(recv.X)) {
					name := "Recv"
					if len(n.Lhs) == 2 {
						name = "RecvOk"
					}
					n.Rhs[0] = chantraceCall(qual, name, recv.X)
					out.Rewrites++
					out.Changed = true
				}
			}
		case *ast.ValueSpec:
			if cfg.RewriteRecv && len(n.Values) == 1 {
				if recv, ok := n.Values[0].(*ast.UnaryExpr); ok && recv.Op == token.ARROW && isChanType(info.TypeOf(recv.X)) {
					n.Values[0] = chantraceCall(qual, "Recv", recv.X)
					out.Rewrites++
					out.Changed = true
				}
			}
		case *ast.RangeStmt:
			if cfg.RewriteRange && isChanType(info.TypeOf(n.X)) {
				n.X = chantraceCall(qual, "Range", n.X)
				out.Rewrites++
				out.Changed = true
			}
		case *ast.UnaryExpr:
			if cfg.RewriteRecv && n.Op == token.ARROW && isChanType(info.TypeOf(n.X)) {
				c.Replace(chantraceCall(qual, "Recv", n.X))
				out.Rewrites++
				out.Changed = true
				return false
			}
		}
		return true
	}, nil)

	if out.Changed && !hasImport {
		astutil.AddImport(fset, file, chantraceImportPath)
	}

	return out
}

func chantraceQualifier(file *ast.File) (qual string, hasImport bool, unusable bool) {
	for _, imp := range file.Imports {
		if imp.Path == nil {
			continue
		}
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		if path != chantraceImportPath {
			continue
		}
		hasImport = true
		if imp.Name == nil {
			return "chantrace", true, false
		}
		switch imp.Name.Name {
		case "_", ".":
			return "", true, true
		default:
			return imp.Name.Name, true, false
		}
	}
	return "chantrace", false, false
}

func chantraceCall(qual, fn string, args ...ast.Expr) ast.Expr {
	return &ast.CallExpr{
		Fun: &ast.SelectorExpr{
			X:   ast.NewIdent(qual),
			Sel: ast.NewIdent(fn),
		},
		Args: args,
	}
}
