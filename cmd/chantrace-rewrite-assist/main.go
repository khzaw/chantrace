package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/khzaw/chantrace/rewriteassist"
	"golang.org/x/tools/go/packages"
)

func main() {
	flag.Parse()
	patterns := flag.Args()
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
		fmt.Fprintf(os.Stderr, "load packages: %v\n", err)
		os.Exit(1)
	}
	if packages.PrintErrors(pkgs) > 0 {
		os.Exit(1)
	}

	total := 0
	for _, pkg := range pkgs {
		for _, file := range pkg.Syntax {
			hints := rewriteassist.CollectFile(pkg.Fset, file, pkg.TypesInfo)
			for _, h := range hints {
				total++
				fmt.Printf("%s\n", h.String())
				fmt.Printf("  suggestion: %s\n", h.Suggestion)
			}
		}
	}

	if total == 0 {
		fmt.Println("no rewrite hints found")
	}
}
