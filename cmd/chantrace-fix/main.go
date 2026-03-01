package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/token"
	"io"
	"os"

	"github.com/khzaw/chantrace/rewriteassist"
	"golang.org/x/tools/go/packages"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("chantrace-fix", flag.ContinueOnError)
	fs.SetOutput(stderr)

	write := fs.Bool("w", false, "write result to (source) file instead of reporting rewrites")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	patterns := fs.Args()
	if len(patterns) == 0 {
		patterns = []string{"./..."}
	}

	cfg := &packages.Config{
		Mode: packages.NeedName |
			packages.NeedFiles |
			packages.NeedCompiledGoFiles |
			packages.NeedSyntax |
			packages.NeedTypes |
			packages.NeedTypesInfo,
	}

	pkgs, err := packages.Load(cfg, patterns...)
	if err != nil {
		fmt.Fprintf(stderr, "load packages: %v\n", err)
		return 1
	}
	if packages.PrintErrors(pkgs) > 0 {
		return 1
	}

	totalSites := 0
	totalFiles := 0

	for _, pkg := range pkgs {
		for i, file := range pkg.Syntax {
			stats := rewriteassist.RewriteFile(pkg.Fset, file, pkg.TypesInfo)
			if stats.Total() == 0 {
				continue
			}

			path := pkg.Fset.Position(file.Package).Filename
			if i < len(pkg.CompiledGoFiles) && pkg.CompiledGoFiles[i] != "" {
				path = pkg.CompiledGoFiles[i]
			}

			if *write {
				if err := writeFile(path, pkg.Fset, file); err != nil {
					fmt.Fprintf(stderr, "rewrite %s: %v\n", path, err)
					return 1
				}
			}

			fmt.Fprintf(
				stdout,
				"%s: rewrote send=%d recv=%d recv_ok=%d range=%d\n",
				path,
				stats.Send,
				stats.Recv,
				stats.RecvOK,
				stats.Range,
			)
			totalFiles++
			totalSites += stats.Total()
		}
	}

	if totalFiles == 0 {
		fmt.Fprintln(stdout, "no rewrites needed")
		return 0
	}
	if *write {
		fmt.Fprintf(stdout, "rewrote %d file(s), %d site(s)\n", totalFiles, totalSites)
		return 0
	}
	fmt.Fprintf(stdout, "found %d file(s), %d site(s) to rewrite (run with -w to apply)\n", totalFiles, totalSites)
	return 0
}

func writeFile(path string, fset *token.FileSet, file *ast.File) error {
	buf, err := formatFile(fset, file)
	if err != nil {
		return err
	}
	return os.WriteFile(path, buf, 0o644)
}

func formatFile(fset *token.FileSet, file *ast.File) ([]byte, error) {
	var b bytes.Buffer
	if err := format.Node(&b, fset, file); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}
