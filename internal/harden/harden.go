// Package harden runs targeted mutation testing over the binding graph:
// implements-bindings say what to break; the union of the witness-role
// bindings of every requirement a symbol implements says which tests must
// notice. Sheets are keyed by symbol — a survivor means no test vouching
// for the body noticed, with no pretence of statement-level requirement
// attribution. Survivors are findings for disposition — this is
// exploration, never gate input; the only records written are kill-sheets
// pinned to body hash and witness set.
package harden

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/backends/golang"
	"github.com/greatliontech/stipulator/internal/records"
)

// Target is one implementation symbol paired with the union of its
// killer tests, derived from the bindings of every requirement that
// binds it as implements.
type Target struct {
	Symbol string
	// Requirements name the implementing claims, for reporting only.
	Requirements []string
	// Witnesses are the unioned witness-role test symbols, canonically
	// ordered — the set the resulting sheet is pinned to.
	Witnesses []string
	// TestPkgs and RunRegex are the go-test execution form of Witnesses.
	TestPkgs []string
	RunRegex string
}

// Survivor is one mutant no bound test noticed.
type Survivor struct {
	Position string
	Operator string
}

// Result is one target's outcome.
type Result struct {
	Symbol        string
	Requirements  []string
	Witnesses     []string
	BodyHash      string
	Mutants       int
	Killed        int
	Discarded     int
	Survivors     []Survivor
	Cached        bool
	SkippedNoTest bool
	// SkippedNotFunc: the symbol resolves but has no function body (a
	// type or variable implements-binding) — nothing to mutate.
	SkippedNotFunc bool
}

// Report is a hardening run's outcome.
type Report struct {
	Results []Result
}

// Plan derives one target per go implements-symbol: the killer set is
// the union of the witness-role (tests or proves) bindings of every
// requirement binding that symbol as implements. A requirement filter
// keeps symbols implementing at least one selected requirement; a symbol
// filter keeps the named symbols (empty = all). A target with no bound
// witnesses is reported skipped, never silently dropped.
func Plan(spec *stipulatorv1.Spec, store *records.Store, reqs, symbols []string) []Target {
	wantReq := toSet(reqs)
	wantSym := toSet(symbols)
	inCorpus := map[string]bool{}
	for _, r := range spec.GetRequirements() {
		inCorpus[r.GetId()] = true
	}

	implReqs := map[string]map[string]bool{}   // symbol -> implementing requirements
	witnesses := map[string]map[string]bool{}  // requirement -> witness test symbols
	for _, bf := range store.Bindings {
		for _, b := range bf.Set.GetBindings() {
			if b.GetBackend() != "go" || !inCorpus[b.GetRequirementId()] {
				continue
			}
			id := b.GetRequirementId()
			switch b.GetRole() {
			case stipulatorv1.BindingRole_BINDING_ROLE_IMPLEMENTS:
				if implReqs[b.GetSymbol()] == nil {
					implReqs[b.GetSymbol()] = map[string]bool{}
				}
				implReqs[b.GetSymbol()][id] = true
			case stipulatorv1.BindingRole_BINDING_ROLE_TESTS,
				stipulatorv1.BindingRole_BINDING_ROLE_PROVES:
				if witnesses[id] == nil {
					witnesses[id] = map[string]bool{}
				}
				witnesses[id][b.GetSymbol()] = true
			}
		}
	}

	syms := make([]string, 0, len(implReqs))
	for sym := range implReqs {
		syms = append(syms, sym)
	}
	sort.Strings(syms)

	var out []Target
	for _, sym := range syms {
		if len(wantSym) > 0 && !wantSym[sym] {
			continue
		}
		t := Target{Symbol: sym}
		for id := range implReqs[sym] {
			t.Requirements = append(t.Requirements, id)
		}
		sort.Strings(t.Requirements)
		if len(wantReq) > 0 && !anyIn(t.Requirements, wantReq) {
			continue
		}

		union := map[string]bool{}
		for _, id := range t.Requirements {
			for w := range witnesses[id] {
				union[w] = true
			}
		}
		pkgs := map[string]bool{}
		fnSet := map[string]bool{}
		for w := range union {
			pkg, fn := splitTestSymbol(w)
			if pkg == "" {
				continue
			}
			t.Witnesses = append(t.Witnesses, w)
			pkgs[pkg] = true
			fnSet[fn] = true
		}
		sort.Strings(t.Witnesses)
		if len(t.Witnesses) > 0 {
			for pkg := range pkgs {
				t.TestPkgs = append(t.TestPkgs, pkg)
			}
			sort.Strings(t.TestPkgs)
			fns := make([]string, 0, len(fnSet))
			for fn := range fnSet {
				fns = append(fns, fn)
			}
			sort.Strings(fns)
			t.RunRegex = "^(" + strings.Join(fns, "|") + ")$"
		}
		out = append(out, t)
	}
	return out
}

func anyIn(items []string, want map[string]bool) bool {
	for _, it := range items {
		if want[it] {
			return true
		}
	}
	return false
}

// Options bound a run.
type Options struct {
	// Budget caps mutants per symbol; 0 means all.
	Budget int
	// Timeout bounds one mutant's test run.
	Timeout time.Duration
	// Force reruns targets whose stored kill-sheet pins still match.
	Force bool
}

// Run mutates each target and executes its witness union per mutant.
// Stored kill-sheets are reused only when both pins hold — the body hash
// and the witness set — unless forced.
func Run(ctx context.Context, dir string, backend *golang.Backend, store *records.Store, targets []Target, opts Options) (*Report, error) {
	if opts.Timeout <= 0 {
		opts.Timeout = 60 * time.Second
	}
	prior := map[string]*stipulatorv1.Hardening{}
	for _, hf := range store.Hardening {
		for _, rec := range hf.Set.GetRecords() {
			prior[rec.GetSymbol()] = rec
		}
	}

	rep := &Report{}
	for _, t := range targets {
		res := Result{Symbol: t.Symbol, Requirements: t.Requirements, Witnesses: t.Witnesses}
		if len(t.TestPkgs) == 0 {
			res.SkippedNoTest = true
			rep.Results = append(rep.Results, res)
			continue
		}
		bodyHash, err := backend.BodyHash(t.Symbol)
		if errors.Is(err, golang.ErrNotFunction) {
			// A type or variable bound as implements is a legitimate
			// static claim with no body to mutate: reported, never fatal,
			// never silently dropped.
			res.SkippedNotFunc = true
			rep.Results = append(rep.Results, res)
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("target %s: %w", t.Symbol, err)
		}
		res.BodyHash = bodyHash
		if rec, ok := prior[t.Symbol]; ok && !opts.Force &&
			rec.GetBodyHash() == bodyHash && slices.Equal(rec.GetWitnesses(), t.Witnesses) {
			res.Cached = true
			res.Mutants = int(rec.GetMutants())
			res.Killed = int(rec.GetKilled())
			for _, s := range rec.GetSurvivors() {
				res.Survivors = append(res.Survivors, Survivor{Position: s.GetPosition(), Operator: s.GetOperator()})
			}
			rep.Results = append(rep.Results, res)
			continue
		}

		mutants, err := backend.Mutants(t.Symbol, opts.Budget)
		if err != nil {
			return nil, fmt.Errorf("target %s: %w", t.Symbol, err)
		}
		// The rapid failfile flag is per-binary, so the witness packages
		// run as two groups: passing it to a binary that does not
		// register it would read as a false kill.
		rapidPkgs, plainPkgs := backend.SplitRapidPkgs(t.TestPkgs)
		groups := []struct {
			pkgs  []string
			flags []string
		}{
			{rapidPkgs, []string{"-rapid.nofailfile"}},
			{plainPkgs, nil},
		}
		for _, m := range mutants {
			outcome := golang.MutantSurvived
			for _, g := range groups {
				if len(g.pkgs) == 0 || outcome != golang.MutantSurvived {
					continue
				}
				out, err := golang.RunMutant(ctx, dir, m, g.pkgs, t.RunRegex, opts.Timeout, g.flags)
				if err != nil {
					return nil, fmt.Errorf("mutant %s %s: %w", m.Position, m.Operator, err)
				}
				outcome = out
			}
			switch outcome {
			case golang.MutantDiscarded:
				res.Discarded++
				continue
			case golang.MutantKilled:
				res.Mutants++
				res.Killed++
			case golang.MutantSurvived:
				res.Mutants++
				res.Survivors = append(res.Survivors, Survivor{Position: m.Position, Operator: m.Operator})
			}
		}
		rep.Results = append(rep.Results, res)
	}
	return rep, nil
}

// Records renders the run's kill-sheets as record-file updates, one file
// per symbol segment, replacing the symbols' prior records. Cached and
// skipped results write nothing.
func (r *Report) Records(store *records.Store) map[string][]byte {
	fresh := map[string][]*stipulatorv1.Hardening{}
	for _, res := range r.Results {
		if res.Cached || res.SkippedNoTest || res.SkippedNotFunc {
			continue
		}
		rec := &stipulatorv1.Hardening{}
		rec.SetBackend("go")
		rec.SetSymbol(res.Symbol)
		rec.SetBodyHash(res.BodyHash)
		rec.SetMutants(int32(res.Mutants))
		rec.SetKilled(int32(res.Killed))
		rec.SetDiscarded(int32(res.Discarded))
		var survivors []*stipulatorv1.MutationSurvivor
		for _, s := range res.Survivors {
			m := &stipulatorv1.MutationSurvivor{}
			m.SetPosition(s.Position)
			m.SetOperator(s.Operator)
			survivors = append(survivors, m)
		}
		rec.SetSurvivors(survivors)
		rec.SetWitnesses(res.Witnesses)
		path := records.HardeningPath(res.Symbol)
		fresh[path] = append(fresh[path], rec)
	}

	out := map[string][]byte{}
	for path, recs := range fresh {
		replaced := map[string]bool{}
		for _, rec := range recs {
			replaced[rec.GetSymbol()] = true
		}
		var all []*stipulatorv1.Hardening
		for _, hf := range store.Hardening {
			if hf.Path != path {
				continue
			}
			for _, rec := range hf.Set.GetRecords() {
				if !replaced[rec.GetSymbol()] {
					all = append(all, rec)
				}
			}
		}
		all = append(all, recs...)
		sort.Slice(all, func(i, j int) bool {
			return all[i].GetSymbol() < all[j].GetSymbol()
		})
		out[path] = records.RenderHardening(all)
	}
	return out
}

func splitTestSymbol(symbol string) (pkg, fn string) {
	i := strings.LastIndex(symbol, ".")
	if i < 0 {
		return "", ""
	}
	return symbol[:i], symbol[i+1:]
}

func toSet(items []string) map[string]bool {
	set := map[string]bool{}
	for _, it := range items {
		if it != "" {
			set[it] = true
		}
	}
	return set
}
