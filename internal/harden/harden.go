// Package harden runs targeted mutation testing over the binding graph:
// each requirement's implements-bindings say what to break, its
// tests-bindings say which tests must notice. Survivors are findings for
// disposition — this is exploration, never gate input; the only records
// written are kill-sheets pinned to body hashes.
package harden

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/backends/golang"
	"github.com/greatliontech/stipulator/internal/records"
)

// Target is one (implementation symbol, killer tests) pair derived from a
// requirement's bindings.
type Target struct {
	Requirement string
	Symbol      string
	// Tests are the bound killer tests: package import path and function.
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
	Requirement   string
	Symbol        string
	BodyHash      string
	Mutants       int
	Killed        int
	Discarded     int
	Survivors     []Survivor
	Cached        bool
	SkippedNoTest bool
}

// Report is a hardening run's outcome.
type Report struct {
	Results []Result
}

// Plan derives targets from the store: for each selected requirement, its
// go implements-symbols paired with its go tests-bindings. Requirements
// and symbols filter (empty = all); a target with no bound tests is
// reported skipped, never silently dropped.
func Plan(spec *stipulatorv1.Spec, store *records.Store, reqs, symbols []string) []Target {
	wantReq := toSet(reqs)
	wantSym := toSet(symbols)
	inCorpus := map[string]bool{}
	for _, r := range spec.GetRequirements() {
		inCorpus[r.GetId()] = true
	}

	impls := map[string][]string{} // requirement -> implements symbols
	tests := map[string]map[string]map[string]bool{}
	for _, bf := range store.Bindings {
		for _, b := range bf.Set.GetBindings() {
			if b.GetBackend() != "go" || !inCorpus[b.GetRequirementId()] {
				continue
			}
			id := b.GetRequirementId()
			switch b.GetRole() {
			case stipulatorv1.BindingRole_BINDING_ROLE_IMPLEMENTS:
				impls[id] = append(impls[id], b.GetSymbol())
			case stipulatorv1.BindingRole_BINDING_ROLE_TESTS:
				pkg, fn := splitTestSymbol(b.GetSymbol())
				if pkg == "" {
					continue
				}
				if tests[id] == nil {
					tests[id] = map[string]map[string]bool{}
				}
				if tests[id][pkg] == nil {
					tests[id][pkg] = map[string]bool{}
				}
				tests[id][pkg][fn] = true
			}
		}
	}

	var out []Target
	ids := make([]string, 0, len(impls))
	for id := range impls {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if len(wantReq) > 0 && !wantReq[id] {
			continue
		}
		syms := append([]string{}, impls[id]...)
		sort.Strings(syms)
		for _, sym := range syms {
			if len(wantSym) > 0 && !wantSym[sym] {
				continue
			}
			t := Target{Requirement: id, Symbol: sym}
			if byPkg := tests[id]; len(byPkg) > 0 {
				var fns []string
				for pkg, set := range byPkg {
					t.TestPkgs = append(t.TestPkgs, pkg)
					for fn := range set {
						fns = append(fns, fn)
					}
				}
				sort.Strings(t.TestPkgs)
				sort.Strings(fns)
				t.RunRegex = "^(" + strings.Join(fns, "|") + ")$"
			}
			out = append(out, t)
		}
	}
	return out
}

// Options bound a run.
type Options struct {
	// Budget caps mutants per symbol; 0 means all.
	Budget int
	// Timeout bounds one mutant's test run.
	Timeout time.Duration
	// Force reruns targets whose stored kill-sheet body hash still matches.
	Force bool
}

// Run mutates each target and executes its bound tests per mutant. Stored
// kill-sheets whose body hash matches are reused unless forced.
func Run(ctx context.Context, dir string, backend *golang.Backend, store *records.Store, targets []Target, opts Options) (*Report, error) {
	if opts.Timeout <= 0 {
		opts.Timeout = 60 * time.Second
	}
	prior := map[string]*stipulatorv1.Hardening{}
	for _, hf := range store.Hardening {
		for _, rec := range hf.Set.GetRecords() {
			prior[rec.GetRequirementId()+"|"+rec.GetSymbol()] = rec
		}
	}

	rep := &Report{}
	for _, t := range targets {
		res := Result{Requirement: t.Requirement, Symbol: t.Symbol}
		if len(t.TestPkgs) == 0 {
			res.SkippedNoTest = true
			rep.Results = append(rep.Results, res)
			continue
		}
		bodyHash, err := backend.BodyHash(t.Symbol)
		if err != nil {
			return nil, fmt.Errorf("target %s: %w", t.Symbol, err)
		}
		res.BodyHash = bodyHash
		if rec, ok := prior[t.Requirement+"|"+t.Symbol]; ok && !opts.Force && rec.GetBodyHash() == bodyHash {
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
		for _, m := range mutants {
			outcome, err := golang.RunMutant(ctx, dir, m, t.TestPkgs, t.RunRegex, opts.Timeout)
			if err != nil {
				return nil, fmt.Errorf("mutant %s %s: %w", m.Position, m.Operator, err)
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
// per requirement segment, replacing that requirement's prior records for
// the symbols run. Cached and skipped results write nothing.
func (r *Report) Records(store *records.Store) map[string][]byte {
	fresh := map[string][]*stipulatorv1.Hardening{}
	for _, res := range r.Results {
		if res.Cached || res.SkippedNoTest {
			continue
		}
		rec := &stipulatorv1.Hardening{}
		rec.SetRequirementId(res.Requirement)
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
		path := records.HardeningPath(res.Requirement)
		fresh[path] = append(fresh[path], rec)
	}

	out := map[string][]byte{}
	for path, recs := range fresh {
		replaced := map[string]bool{}
		for _, rec := range recs {
			replaced[rec.GetRequirementId()+"|"+rec.GetSymbol()] = true
		}
		var all []*stipulatorv1.Hardening
		for _, hf := range store.Hardening {
			if hf.Path != path {
				continue
			}
			for _, rec := range hf.Set.GetRecords() {
				if !replaced[rec.GetRequirementId()+"|"+rec.GetSymbol()] {
					all = append(all, rec)
				}
			}
		}
		all = append(all, recs...)
		sort.Slice(all, func(i, j int) bool {
			if all[i].GetRequirementId() != all[j].GetRequirementId() {
				return all[i].GetRequirementId() < all[j].GetRequirementId()
			}
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
