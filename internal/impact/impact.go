// Package impact previews what the working tree's change set against
// HEAD plausibly touches: requirements whose spec content moved (through
// the diff semantics), requirements bound to symbols declared in changed
// source files, and the witness subjects whose packages the change
// reaches through the import graph and compile-time embed couplings.
// The preview executes no test and
// claims no freshness verdict — it names candidates for the witnessed
// surfaces to decide, and its omissions are bounded by what symbol
// resolution and import reach can see and by the backends the preview
// implements (bindings on other backends are counted as unconsulted,
// never silently dropped), so an empty preview is advisory,
// never proof of no impact (REQ-change-impact). Symbols resolve in the
// working tree alone: a pure code deletion leaves nothing to resolve
// and its candidates surface at verification, while a spec-side
// deletion does report — the committed corpus still names it. Version-control access
// stays behind the gitfs adapter (REQ-core-vcs-free).
package impact

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"sort"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/backends/golang"
	"github.com/greatliontech/stipulator/internal/compile"
	"github.com/greatliontech/stipulator/internal/diff"
	"github.com/greatliontech/stipulator/internal/gitfs"
	"github.com/greatliontech/stipulator/internal/records"
)

// BoundHit is one binding whose symbol declares in a changed file.
type BoundHit struct {
	Requirement string
	Symbol      string
	File        string
	Role        stipulatorv1.BindingRole
}

// WitnessHit is one witness subject — a tests- or proves-role binding —
// whose package the change set reaches through the import graph.
type WitnessHit struct {
	Requirement string
	Symbol      string
}

// Report is the preview: candidates, never verdicts.
type Report struct {
	// Changed is the whole worktree-vs-HEAD change set, corpus-scoped.
	Changed []string
	// Spec is the per-identity delta between the corpus as committed at
	// HEAD and the working tree.
	Spec *diff.Report
	// Bound lists bindings whose symbols declare in changed files,
	// ordered by requirement then symbol.
	Bound []BoundHit
	// Witnesses lists witness subjects whose packages the change reaches,
	// ordered by requirement then symbol.
	Witnesses []WitnessHit
	// Unconsulted counts bindings on backends the preview does not
	// implement: outside the candidate set by bound, never silently.
	Unconsulted int
}

// Preview computes the impact preview for the corpus rooted at dir. It
// reads the change set through the VCS adapter, diffs the committed
// corpus against the working tree, and joins the working tree's binding
// records with symbol resolution and import reach. Nothing executes;
// nothing is judged fresh or stale.
func Preview(ctx context.Context, dir string) (*Report, error) {
	changed, err := gitfs.Changed(dir)
	if err != nil {
		return nil, err
	}
	r := &Report{Changed: changed}

	// Spec side: the committed contract at HEAD against the working tree,
	// compared per identity so a pure reorganization previews as no
	// semantic delta. Either corpus failing to compile is an operational
	// fault — a preview over an uncompilable contract would be guessing.
	headFS, err := gitfs.FS(dir, "HEAD")
	if err != nil {
		return nil, fmt.Errorf("no HEAD corpus to preview against (the last commit is the baseline): %w", err)
	}
	oldSpec, err := compileClean(headFS, "HEAD")
	if err != nil {
		return nil, err
	}
	newSpec, err := compileClean(os.DirFS(dir), "working tree")
	if err != nil {
		return nil, err
	}
	r.Spec = diff.Diff(oldSpec, newSpec)

	// The records are read from the working tree — uncommitted binding
	// edits participate, exactly like uncommitted code: the preview is an
	// edit-time instrument, not a committed-state audit.
	store, err := records.Load(os.DirFS(dir))
	if err != nil {
		return nil, err
	}
	goBound := false
	for _, bf := range store.Bindings {
		for _, b := range bf.Set.GetBindings() {
			if b.GetBackend() == "go" {
				goBound = true
			} else {
				r.Unconsulted++
			}
		}
	}
	// No Go bindings — or no changed files at all — means symbol
	// resolution requires nothing: the code-side candidates derive solely
	// from the change set, so a spec-only corpus or a clean tree previews
	// without a Go toolchain in sight (REQ-change-impact's loading
	// bound). When a load is due it runs behind the owned resolver
	// boundary, so cancelling the preview kills the package launcher's
	// whole descendant tree (REQ-go-owned-processes).
	if !goBound || len(changed) == 0 {
		return r, nil
	}
	be, err := golang.NewOwned(ctx, dir)
	if err != nil {
		return nil, err
	}
	defer be.Close()

	changedSet := make(map[string]bool, len(changed))
	for _, f := range changed {
		changedSet[f] = true
	}
	reach, err := be.ReachedPackages(changed)
	if err != nil {
		return nil, err
	}
	for _, bf := range store.Bindings {
		for _, b := range bf.Set.GetBindings() {
			// Only the Go backend resolves here; other backends' symbols
			// are among the preview's declared omissions.
			if b.GetBackend() != "go" {
				continue
			}
			file, ok, err := be.SymbolFile(b.GetSymbol())
			if err != nil {
				return nil, err
			}
			if ok && changedSet[file] {
				r.Bound = append(r.Bound, BoundHit{
					Requirement: b.GetRequirementId(),
					Symbol:      b.GetSymbol(),
					File:        file,
					Role:        b.GetRole(),
				})
			}
			switch b.GetRole() {
			case stipulatorv1.BindingRole_BINDING_ROLE_TESTS, stipulatorv1.BindingRole_BINDING_ROLE_PROVES:
				pkg, err := be.SymbolPackage(b.GetSymbol())
				if err != nil {
					return nil, err
				}
				if reach[pkg] {
					r.Witnesses = append(r.Witnesses, WitnessHit{
						Requirement: b.GetRequirementId(),
						Symbol:      b.GetSymbol(),
					})
				}
			}
		}
	}
	sort.Slice(r.Bound, func(i, j int) bool {
		if r.Bound[i].Requirement != r.Bound[j].Requirement {
			return r.Bound[i].Requirement < r.Bound[j].Requirement
		}
		return r.Bound[i].Symbol < r.Bound[j].Symbol
	})
	sort.Slice(r.Witnesses, func(i, j int) bool {
		if r.Witnesses[i].Requirement != r.Witnesses[j].Requirement {
			return r.Witnesses[i].Requirement < r.Witnesses[j].Requirement
		}
		return r.Witnesses[i].Symbol < r.Witnesses[j].Symbol
	})
	return r, nil
}

// compileClean compiles one corpus and turns compile-error diagnostics
// into an error naming the tree they came from: the preview refuses to
// guess over a broken contract.
func compileClean(fsys fs.FS, label string) (*stipulatorv1.Spec, error) {
	spec, diags, err := compile.Compile(fsys)
	if err != nil {
		return nil, fmt.Errorf("compiling the %s corpus: %w", label, err)
	}
	if errs := compile.Errors(diags); len(errs) > 0 {
		return nil, fmt.Errorf("the %s corpus does not compile: %s:%d: %s",
			label, errs[0].Document, errs[0].Line, errs[0].Message)
	}
	return spec, nil
}

// SpecTouched flattens the semantic identity delta — added, removed,
// text-changed, kind-changed requirements — into one sorted id list for
// callers that need "which requirements moved" without the axes.
func (r *Report) SpecTouched() []string {
	seen := map[string]bool{}
	for _, list := range [][]string{
		r.Spec.AddedRequirements, r.Spec.RemovedRequirements,
		r.Spec.TextChangedRequirements, r.Spec.KindChangedRequirements,
	} {
		for _, id := range list {
			seen[id] = true
		}
	}
	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
