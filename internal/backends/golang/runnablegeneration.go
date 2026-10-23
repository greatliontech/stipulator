package golang

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"go/ast"
	"go/doc"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"sort"

	"github.com/greatliontech/gofresh"
)

// witnessSnapshot couples the native source generation with its independently
// derived runnable population. The policy's conserved obligations are not this
// population: later source generations can compile additional runnables.
type witnessSnapshot struct {
	*gofresh.Snapshot
	roster *runnableGeneration
}

type runnableSource struct {
	file   string
	digest [32]byte
}

type runnablePackage struct {
	name, dir string
	runnables []Obligation
	sources   []runnableSource
}

// readPackageRunnables is the single syntax rule shared by policy discovery and
// native generation preparation. Discovery can retain its partial inventory beside
// a build failure; native preparation must refuse the accompanying error.
func readPackageRunnables(ctx context.Context, p listedPackage) (runnablePackage, error) {
	result := runnablePackage{name: p.Name, dir: p.Dir}
	var faults []error
	if p.Error != nil {
		faults = append(faults, fmt.Errorf("package %s: %s", p.ImportPath, p.Error.Err))
	}
	files := append(slices.Clone(p.TestGoFiles), p.XTestGoFiles...)
	sort.Strings(files)
	files = slices.Compact(files)
	fset := token.NewFileSet()
	var parsed []*ast.File
	for _, name := range files {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		path := filepath.Join(p.Dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			faults = append(faults, err)
			continue
		}
		result.sources = append(result.sources, runnableSource{file: name, digest: sha256.Sum256(data)})
		file, err := parser.ParseFile(fset, path, data, parser.ParseComments)
		if err != nil {
			faults = append(faults, err)
			continue
		}
		parsed = append(parsed, file)
		for _, declaration := range file.Decls {
			fn, ok := declaration.(*ast.FuncDecl)
			if !ok {
				continue
			}
			kind := ObligationKind("")
			switch {
			case syntacticTestFunc(fn, "Test", "T"):
				kind = ObligationTest
			case syntacticTestFunc(fn, "Fuzz", "F"):
				kind = ObligationFuzz
			}
			if kind != "" {
				result.runnables = append(result.runnables, Obligation{Kind: kind, Package: p.ImportPath, Name: fn.Name.Name})
			}
		}
	}
	for _, example := range doc.Examples(parsed...) {
		if example.Output != "" || example.EmptyOutput {
			result.runnables = append(result.runnables, Obligation{Kind: ObligationExample, Package: p.ImportPath, Name: "Example" + example.Name})
		}
	}
	slices.SortFunc(result.runnables, func(a, b Obligation) int { return compareObligation(a, b) })
	result.runnables = slices.Compact(result.runnables)
	return result, errors.Join(faults...)
}

func compareObligation(a, b Obligation) int {
	if a.ID() < b.ID() {
		return -1
	}
	if a.ID() > b.ID() {
		return 1
	}
	return 0
}

type runnableGeneration struct {
	query    NormalizedInvocation
	packages map[string]runnablePackage
}

func observeRunnableGeneration(ctx context.Context, query NormalizedInvocation) (*runnableGeneration, error) {
	listed, err := readPackageListing(ctx, &query, engineRunner, "ImportPath,Name,Dir,TestGoFiles,XTestGoFiles,Error")
	if err != nil {
		return nil, err
	}
	result := &runnableGeneration{query: query, packages: map[string]runnablePackage{}}
	for _, p := range listed {
		if !slices.Contains(query.Packages, p.ImportPath) || p.Name == "" || p.Dir == "" {
			return nil, fmt.Errorf("native runnable metadata incomplete or outside the declared package set: %s", p.ImportPath)
		}
		if _, duplicate := result.packages[p.ImportPath]; duplicate {
			return nil, fmt.Errorf("duplicate native runnable metadata for %s", p.ImportPath)
		}
		inventory, err := readPackageRunnables(ctx, p)
		if err != nil {
			return nil, err
		}
		result.packages[p.ImportPath] = inventory
	}
	if len(result.packages) != len(query.Packages) {
		return nil, fmt.Errorf("native runnable metadata did not cover every declared package")
	}
	return result, ctx.Err()
}

func (g *runnableGeneration) subjects() []gofresh.Subject {
	var subjects []gofresh.Subject
	for pkg, inventory := range g.packages {
		for _, runnable := range inventory.runnables {
			subjects = append(subjects, gofresh.Subject{Package: pkg, Symbol: runnable.Name})
		}
	}
	sortSubjects(subjects)
	return subjects
}

// unchanged closes the observation surrounding native snapshot construction.
// Bytes and selected file membership agree at both ends; native construction
// supplies the source/build agreement pair inside that same span.
func (g *runnableGeneration) unchanged(ctx context.Context) error {
	current, err := observeRunnableGeneration(ctx, g.query)
	if err != nil {
		return err
	}
	for _, pkg := range g.query.Packages {
		before, after := g.packages[pkg], current.packages[pkg]
		if before.name != after.name || before.dir != after.dir || !slices.Equal(before.sources, after.sources) || !slices.Equal(before.runnables, after.runnables) {
			return fmt.Errorf("%w: runnable generation changed during capture for %s", gofresh.ErrViewChanged, pkg)
		}
	}
	return nil
}

// currentNames validates population, not unchanged source bytes. Body or helper
// edits are the native source guard's concern; an unchanged runnable population
// in a newly captured generation retains full producing capability.
func (g *runnableGeneration) currentNames(ctx context.Context, pkg string) ([]string, error) {
	if g == nil {
		return nil, fmt.Errorf("native snapshot has no generation-bound runnable inventory")
	}
	before, ok := g.packages[pkg]
	if !ok {
		return nil, fmt.Errorf("package %s is outside the native runnable generation", pkg)
	}
	query := g.query
	query.Packages = []string{pkg}
	current, err := observeRunnableGeneration(ctx, query)
	if err != nil {
		return nil, err
	}
	after := current.packages[pkg]
	if before.name != after.name || before.dir != after.dir || !slices.Equal(before.runnables, after.runnables) {
		return nil, fmt.Errorf("%w: runnable population changed before process registration for %s", gofresh.ErrViewChanged, pkg)
	}
	var names []string
	for _, runnable := range before.runnables {
		names = append(names, runnable.Name)
	}
	return names, nil
}
