package main

import (
	"github.com/khzaw/chantrace/analysis/chantracecheck"
	"golang.org/x/tools/go/analysis/singlechecker"
)

func main() {
	singlechecker.Main(chantracecheck.Analyzer)
}
