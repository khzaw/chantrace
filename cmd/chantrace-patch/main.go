package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/khzaw/chantrace/rewriteassist"
	"golang.org/x/tools/go/packages"
)

const (
	patchesDirName   = ".chantrace/patches"
	activePatchFile  = "ACTIVE"
	manifestFileName = "manifest.json"
)

type manifest struct {
	ID        string         `json:"id"`
	CreatedAt string         `json:"created_at"`
	Root      string         `json:"root"`
	Patterns  []string       `json:"patterns"`
	Files     []manifestFile `json:"files"`
	Issues    []manifestNote `json:"issues,omitempty"`
}

type manifestFile struct {
	Path         string `json:"path"`
	BackupPath   string `json:"backup_path"`
	SHA256Before string `json:"sha256_before"`
	SHA256After  string `json:"sha256_after"`
	Rewrites     int    `json:"rewrites"`
}

type manifestNote struct {
	Path    string `json:"path"`
	Line    int    `json:"line"`
	Column  int    `json:"column"`
	Message string `json:"message"`
}

type plannedFile struct {
	absPath  string
	relPath  string
	before   []byte
	after    []byte
	rewrites int
	fileMode os.FileMode
}

type plan struct {
	files  []plannedFile
	issues []manifestNote
}

func main() {
	if len(os.Args) < 2 {
		usage(os.Stderr)
		os.Exit(2)
	}

	var code int
	switch os.Args[1] {
	case "apply":
		code = runApply(os.Args[2:])
	case "revert":
		code = runRevert(os.Args[2:])
	case "status":
		code = runStatus(os.Args[2:])
	case "-h", "--help", "help":
		usage(os.Stdout)
		code = 0
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n\n", os.Args[1])
		usage(os.Stderr)
		code = 2
	}
	os.Exit(code)
}

func usage(w *os.File) {
	fmt.Fprintln(w, "chantrace-patch: reversible chantrace codemod workflow")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  chantrace-patch apply [--dry-run] [packages...]")
	fmt.Fprintln(w, "  chantrace-patch status")
	fmt.Fprintln(w, "  chantrace-patch revert [--force]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Examples:")
	fmt.Fprintln(w, "  go run ./cmd/chantrace-patch apply ./...")
	fmt.Fprintln(w, "  go run ./cmd/chantrace-patch status")
	fmt.Fprintln(w, "  go run ./cmd/chantrace-patch revert")
}

func runApply(args []string) int {
	fs := flag.NewFlagSet("apply", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var dryRun bool
	fs.BoolVar(&dryRun, "dry-run", false, "print planned changes without writing files")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	patterns := fs.Args()
	if len(patterns) == 0 {
		patterns = []string{"./..."}
	}

	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "pwd: %v\n", err)
		return 1
	}
	root, err = filepath.Abs(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "abs root: %v\n", err)
		return 1
	}

	if !dryRun {
		active, err := readActivePatchID(root)
		if err != nil {
			fmt.Fprintf(os.Stderr, "read active patch: %v\n", err)
			return 1
		}
		if active != "" {
			fmt.Fprintf(os.Stderr, "active patch %q already exists; revert it before applying a new patch\n", active)
			return 1
		}
	}

	pkgs, err := loadPackages(patterns)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load packages: %v\n", err)
		return 1
	}

	plan, err := buildPlan(root, pkgs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "build patch plan: %v\n", err)
		return 1
	}

	sort.Slice(plan.files, func(i, j int) bool {
		return plan.files[i].relPath < plan.files[j].relPath
	})
	sort.Slice(plan.issues, func(i, j int) bool {
		if plan.issues[i].Path != plan.issues[j].Path {
			return plan.issues[i].Path < plan.issues[j].Path
		}
		if plan.issues[i].Line != plan.issues[j].Line {
			return plan.issues[i].Line < plan.issues[j].Line
		}
		return plan.issues[i].Column < plan.issues[j].Column
	})

	if len(plan.files) == 0 {
		fmt.Println("no rewrite changes needed")
		if len(plan.issues) > 0 {
			fmt.Printf("manual notes: %d\n", len(plan.issues))
			printIssues(plan.issues)
		}
		return 0
	}

	fmt.Printf("planned file rewrites: %d\n", len(plan.files))
	if len(plan.issues) > 0 {
		fmt.Printf("manual notes: %d\n", len(plan.issues))
	}
	for _, f := range plan.files {
		fmt.Printf("  %s (%d rewrites)\n", f.relPath, f.rewrites)
	}
	if len(plan.issues) > 0 {
		printIssues(plan.issues)
	}

	if dryRun {
		return 0
	}

	patchID := time.Now().UTC().Format("20060102T150405Z")
	patchDir := filepath.Join(root, patchesDirName, patchID)
	origDir := filepath.Join(patchDir, "original")
	if err := os.MkdirAll(origDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "mkdir patch dir: %v\n", err)
		return 1
	}

	m := manifest{
		ID:        patchID,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Root:      root,
		Patterns:  append([]string(nil), patterns...),
		Issues:    plan.issues,
	}

	for _, f := range plan.files {
		backupAbs := filepath.Join(origDir, filepath.FromSlash(f.relPath))
		if err := os.MkdirAll(filepath.Dir(backupAbs), 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "mkdir backup dir for %s: %v\n", f.relPath, err)
			return 1
		}
		if err := os.WriteFile(backupAbs, f.before, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "write backup for %s: %v\n", f.relPath, err)
			return 1
		}
		if err := os.WriteFile(f.absPath, f.after, f.fileMode); err != nil {
			fmt.Fprintf(os.Stderr, "write rewritten file %s: %v\n", f.relPath, err)
			return 1
		}

		m.Files = append(m.Files, manifestFile{
			Path:         f.relPath,
			BackupPath:   filepath.ToSlash(strings.TrimPrefix(backupAbs, patchDir+string(os.PathSeparator))),
			SHA256Before: hashBytes(f.before),
			SHA256After:  hashBytes(f.after),
			Rewrites:     f.rewrites,
		})
	}

	if err := writeManifest(patchDir, m); err != nil {
		fmt.Fprintf(os.Stderr, "write manifest: %v\n", err)
		return 1
	}
	if err := writeActivePatchID(root, patchID); err != nil {
		fmt.Fprintf(os.Stderr, "write active patch: %v\n", err)
		return 1
	}

	fmt.Printf("applied patch %s (%d files)\n", patchID, len(m.Files))
	return 0
}

func runRevert(args []string) int {
	fs := flag.NewFlagSet("revert", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var force bool
	fs.BoolVar(&force, "force", false, "revert even if files changed since patch apply")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "revert does not accept package patterns")
		return 2
	}

	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "pwd: %v\n", err)
		return 1
	}
	root, err = filepath.Abs(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "abs root: %v\n", err)
		return 1
	}

	patchID, err := readActivePatchID(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read active patch: %v\n", err)
		return 1
	}
	if patchID == "" {
		fmt.Println("no active patch")
		return 0
	}

	patchDir := filepath.Join(root, patchesDirName, patchID)
	m, err := readManifest(patchDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read manifest for patch %s: %v\n", patchID, err)
		return 1
	}

	drifted, err := checkDrift(root, m)
	if err != nil {
		fmt.Fprintf(os.Stderr, "check drift: %v\n", err)
		return 1
	}
	if len(drifted) > 0 && !force {
		fmt.Fprintf(os.Stderr, "refusing revert: %d file(s) changed after patch apply:\n", len(drifted))
		for _, p := range drifted {
			fmt.Fprintf(os.Stderr, "  %s\n", p)
		}
		fmt.Fprintln(os.Stderr, "use --force to restore backups anyway")
		return 1
	}

	for _, f := range m.Files {
		backupAbs := filepath.Join(patchDir, filepath.FromSlash(f.BackupPath))
		content, err := os.ReadFile(backupAbs)
		if err != nil {
			fmt.Fprintf(os.Stderr, "read backup %s: %v\n", f.Path, err)
			return 1
		}
		target := filepath.Join(root, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "mkdir target dir for %s: %v\n", f.Path, err)
			return 1
		}
		info, statErr := os.Stat(target)
		mode := os.FileMode(0o644)
		if statErr == nil {
			mode = info.Mode().Perm()
		}
		if err := os.WriteFile(target, content, mode); err != nil {
			fmt.Fprintf(os.Stderr, "restore %s: %v\n", f.Path, err)
			return 1
		}
	}

	if err := clearActivePatchID(root); err != nil {
		fmt.Fprintf(os.Stderr, "clear active patch: %v\n", err)
		return 1
	}

	fmt.Printf("reverted patch %s (%d files)\n", patchID, len(m.Files))
	return 0
}

func runStatus(args []string) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "status does not accept package patterns")
		return 2
	}

	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "pwd: %v\n", err)
		return 1
	}
	root, err = filepath.Abs(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "abs root: %v\n", err)
		return 1
	}

	patchID, err := readActivePatchID(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read active patch: %v\n", err)
		return 1
	}
	if patchID == "" {
		fmt.Println("no active patch")
		return 0
	}

	patchDir := filepath.Join(root, patchesDirName, patchID)
	m, err := readManifest(patchDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read manifest for patch %s: %v\n", patchID, err)
		return 1
	}
	drifted, err := checkDrift(root, m)
	if err != nil {
		fmt.Fprintf(os.Stderr, "check drift: %v\n", err)
		return 1
	}

	fmt.Printf("active patch: %s\n", patchID)
	fmt.Printf("created at: %s\n", m.CreatedAt)
	fmt.Printf("files: %d\n", len(m.Files))
	fmt.Printf("manual notes: %d\n", len(m.Issues))
	if len(drifted) == 0 {
		fmt.Println("drift: clean")
		return 0
	}
	fmt.Printf("drift: %d file(s)\n", len(drifted))
	for _, p := range drifted {
		fmt.Printf("  %s\n", p)
	}
	return 0
}

func loadPackages(patterns []string) ([]*packages.Package, error) {
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
		return nil, err
	}
	if n := packages.PrintErrors(pkgs); n > 0 {
		return nil, fmt.Errorf("%d package load errors", n)
	}
	return pkgs, nil
}

func buildPlan(root string, pkgs []*packages.Package) (plan, error) {
	cfg := rewriteassist.DefaultRewriteConfig()
	var out plan

	seen := make(map[string]struct{})
	for _, pkg := range pkgs {
		for _, file := range pkg.Syntax {
			filename := pkg.Fset.Position(file.Pos()).Filename
			if filename == "" {
				continue
			}
			abs, err := filepath.Abs(filename)
			if err != nil {
				return out, fmt.Errorf("abs path for %s: %w", filename, err)
			}
			if _, ok := seen[abs]; ok {
				continue
			}
			seen[abs] = struct{}{}

			res := rewriteassist.RewriteFile(pkg.Fset, file, pkg.TypesInfo, cfg)
			for _, issue := range res.Issues {
				issuePath := issue.Position.Filename
				if issuePath == "" {
					issuePath = abs
				}
				rel := relPath(root, issuePath)
				out.issues = append(out.issues, manifestNote{
					Path:    rel,
					Line:    issue.Position.Line,
					Column:  issue.Position.Column,
					Message: issue.Message,
				})
			}
			if !res.Changed {
				continue
			}

			before, err := os.ReadFile(abs)
			if err != nil {
				return out, fmt.Errorf("read source %s: %w", abs, err)
			}

			var b bytes.Buffer
			if err := format.Node(&b, pkg.Fset, file); err != nil {
				return out, fmt.Errorf("format rewritten %s: %w", abs, err)
			}
			after := b.Bytes()
			if !bytes.HasSuffix(after, []byte("\n")) {
				after = append(after, '\n')
			}
			if bytes.Equal(before, after) {
				continue
			}

			info, err := os.Stat(abs)
			if err != nil {
				return out, fmt.Errorf("stat source %s: %w", abs, err)
			}
			rel := relPath(root, abs)
			out.files = append(out.files, plannedFile{
				absPath:  abs,
				relPath:  rel,
				before:   before,
				after:    after,
				rewrites: res.Rewrites,
				fileMode: info.Mode().Perm(),
			})
		}
	}

	return out, nil
}

func printIssues(issues []manifestNote) {
	fmt.Println("manual notes:")
	for _, n := range issues {
		if n.Line > 0 {
			fmt.Printf("  %s:%d:%d: %s\n", n.Path, n.Line, n.Column, n.Message)
			continue
		}
		fmt.Printf("  %s: %s\n", n.Path, n.Message)
	}
}

func readActivePatchID(root string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, patchesDirName, activePatchFile))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func writeActivePatchID(root, id string) error {
	base := filepath.Join(root, patchesDirName)
	if err := os.MkdirAll(base, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(base, activePatchFile), []byte(id+"\n"), 0o644)
}

func clearActivePatchID(root string) error {
	err := os.Remove(filepath.Join(root, patchesDirName, activePatchFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func writeManifest(patchDir string, m manifest) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(filepath.Join(patchDir, manifestFileName), data, 0o644)
}

func readManifest(patchDir string) (manifest, error) {
	var m manifest
	data, err := os.ReadFile(filepath.Join(patchDir, manifestFileName))
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return m, err
	}
	return m, nil
}

func checkDrift(root string, m manifest) ([]string, error) {
	drifted := make([]string, 0)
	for _, f := range m.Files {
		p := filepath.Join(root, filepath.FromSlash(f.Path))
		content, err := os.ReadFile(p)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				drifted = append(drifted, f.Path+" (missing)")
				continue
			}
			return nil, fmt.Errorf("read %s: %w", f.Path, err)
		}
		if hashBytes(content) != f.SHA256After {
			drifted = append(drifted, f.Path)
		}
	}
	sort.Strings(drifted)
	return drifted, nil
}

func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func relPath(root, target string) string {
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return filepath.ToSlash(target)
	}
	rel, err := filepath.Rel(root, absTarget)
	if err != nil || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || rel == ".." {
		return filepath.ToSlash(absTarget)
	}
	return filepath.ToSlash(rel)
}
