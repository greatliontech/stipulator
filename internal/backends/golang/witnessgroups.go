package golang

import (
	"context"
	"sort"

	gofresh "github.com/greatliontech/gofresh"
	"github.com/greatliontech/stipulator/internal/witnesscache"
)

// witnessGroup is one capture group's serving state: the analysis views
// over its in-policy subjects, the cached records found for them, and the
// served/stale partition the pre-execution fingerprint check produced.
type witnessGroup struct {
	g *captureGroup
	// view is the group's one analysis view: the expensive observation,
	// paid once per group, whose facts every package's leg shares;
	// released once every leg has.
	view *witnessSnapshot
	// legs is the publication state per PACKAGE — the unit of
	// persistence (REQ-policy-cancellation-unit): the package's serving
	// checks and captures start on its own sibling of view. Publication
	// and serving close independently over shared facts; the leg releases
	// once the package has published and, where it serves, revalidated.
	legs map[string]*packageLeg
	// executedWhy names, per stale subject that held prior evidence, why
	// its variants failed to serve - the last-checked variant's verdict
	// reason, movers named by gofresh's attribution.
	executedWhy map[gofresh.Subject]executedReason
	// served are the subjects whose recorded fingerprint checked valid
	// before execution; stale maps each package to its executing top-level
	// names, sorted.
	served []gofresh.Subject
	stale  map[string][]string
}

// packages lists the group's packages in order: every package holding a
// served or a stale subject.
func (wg *witnessGroup) packages() []string {
	seen := map[string]bool{}
	var out []string
	for pkg := range wg.stale {
		if !seen[pkg] {
			seen[pkg] = true
			out = append(out, pkg)
		}
	}
	for _, s := range wg.served {
		if !seen[s.Package] {
			seen[s.Package] = true
			out = append(out, s.Package)
		}
	}
	sort.Strings(out)
	return out
}

// servedIn is the group's served subjects of one package.
func (wg *witnessGroup) servedIn(pkg string) []gofresh.Subject {
	var out []gofresh.Subject
	for _, s := range wg.served {
		if s.Package == pkg {
			out = append(out, s)
		}
	}
	return out
}

func (wg *witnessGroup) selectedRecord(s gofresh.Subject) witnesscache.Record {
	return wg.legs[s.Package].chosen[s].record
}

// roundCandidates selects round N's checkable fingerprints: each
// unproven subject's Nth variant, gated on its licensing exclusions. A
// gate-refused variant counts as round progress — refusal must never
// end the walk while later variants remain, or a poisoned early
// variant would permanently mask a serveable one (spurious re-run,
// forever). A record's observation proves nothing about the surfaces
// its capture-time exclusions elided, so it serves only while the
// current policy still asserts every one of them: a withdrawn
// exclusion re-runs the witnesses it licensed, while an added
// exclusion serves existing evidence unchanged (its identities are in
// the manifest and simply revalidate). A record's reviewed scratch
// namespaces serve the same way: a read inside one entered no identity,
// so the record serves only while the current policy still declares
// each — a withdrawn or narrowed namespace re-executes the witnesses it
// licensed, an added one serves existing evidence unchanged.
func roundCandidates(subjects []gofresh.Subject, cached map[string][]witnesscache.Record, chosen map[gofresh.Subject]chosenWitness, round int, declarations recordedDeclarations, refused func(gofresh.Subject, executedReason)) (map[gofresh.Subject]gofresh.Fingerprint, bool) {
	fps := map[gofresh.Subject]gofresh.Fingerprint{}
	advanced := false
	for _, s := range subjects {
		vars := cached[s.Package+"."+s.Symbol]
		if _, selected := chosen[s]; selected || round >= len(vars) {
			continue
		}
		advanced = true
		if why := declarations.refusal(vars[round]); why != (executedReason{}) {
			refused(s, why)
			continue
		}
		fps[s] = vars[round].Fingerprint
	}
	return fps, advanced
}

// The serving rounds' gate refusals, each the re-execution reason
// attributed to the subject whose variant it refused.
const (
	refusedWithdrawnExclusion = "recorded under a withdrawn observation exclusion"
	refusedWithdrawnNamespace = "recorded under a withdrawn scratch namespace"
)

// prepareWitnessGroups builds each group's shared native snapshot and independent
// package checking transactions. Variant decisions are provisional until those
// transactions close after execution. Faults split by classifyFault: a
// toolchain-provenance refusal returns as the error — a run-level
// abort, never a degradation — while any other fault returns the
// degraded reason: the caller serves nothing and executes everything
// covered (REQ-evidence-freshness-degrade).
func prepareWitnessGroups(ctx context.Context, dir string, d *policyDiscovery, cached map[string][]witnesscache.Record) ([]*witnessGroup, string, error) {
	var out []*witnessGroup
	var prepared []*packageLeg
	complete := false
	defer func() {
		if !complete {
			for _, leg := range prepared {
				leg.dispose()
			}
			for _, g := range d.groups {
				g.sessionState = nil
			}
		}
	}()
	for g, subjects := range d.populatedGroups() {
		g.sessionState = &groupSessionState{}
		declarations := declarationsFor(g)
		view, inputs, err := groupSnapshot(ctx, dir, g)
		if err != nil {
			if abort, reason := classifyFault(err); !abort {
				return nil, reason, nil
			}
			return nil, "", err
		}
		wg := &witnessGroup{
			g: g, view: view, legs: map[string]*packageLeg{},
			executedWhy: map[gofresh.Subject]executedReason{},
			stale:       map[string][]string{},
		}
		// Every package binds its actual prospective runtime over the shared
		// snapshot. Its historical checking transaction closes independently
		// of every producing attempt and retains all candidate rounds.
		byPkg := map[string][]gofresh.Subject{}
		for _, s := range subjects {
			byPkg[s.Package] = append(byPkg[s.Package], s)
		}
		pkgs := make([]string, 0, len(byPkg))
		for pkg := range byPkg {
			pkgs = append(pkgs, pkg)
		}
		sort.Strings(pkgs)
		for _, pkg := range pkgs {
			subjects := byPkg[pkg]
			var invocation *NormalizedInvocation
			for _, ic := range d.invocations {
				if ic.n.Name == g.selectingInvocation(pkg) {
					invocation = ic.n
					break
				}
			}
			leg, err := nativePackageLeg(ctx, wg.view, inputs, invocation, pkg)
			if err != nil {
				if abort, reason := classifyFault(err); !abort {
					return nil, reason, nil
				}
				return nil, "", err
			}
			wg.legs[pkg] = leg
			prepared = append(prepared, leg)
			var batchSubjects, soloSubjects []gofresh.Subject
			for _, s := range subjects {
				if wg.view.isSolo(s) {
					soloSubjects = append(soloSubjects, s)
				} else {
					batchSubjects = append(batchSubjects, s)
				}
			}
			if len(batchSubjects) > 0 {
				leg.checking, err = leg.binding.BeginCheck(batchSubjects)
				if err != nil {
					return nil, err.Error(), nil
				}
			}
			if len(soloSubjects) > 0 {
				leg.soloChecking, err = leg.soloBinding.BeginCheck(soloSubjects)
				if err != nil {
					return nil, err.Error(), nil
				}
			}
			leg.chosen = map[gofresh.Subject]chosenWitness{}
			serving, groupCached := servingCandidates(g.id, subjects, g.neverServes, cached, wg.executedWhy)
			checkRound := func(unit *gofresh.CheckingUnit, isSolo bool, candidates []gofresh.Candidate, round int) (string, error) {
				if len(candidates) == 0 {
					return "", nil
				}
				verdicts, err := unit.CheckBatch(ctx, candidates)
				if err != nil {
					if abort, reason := classifyFault(err); !abort {
						return reason, nil
					}
					return "", err
				}
				for _, checked := range verdicts {
					s := checked.Subject
					if checked.Verdict.Status == gofresh.Valid {
						leg.chosen[s] = chosenWitness{index: checked.Index, solo: isSolo, record: groupCached[s.Package+"."+s.Symbol][round]}
						delete(wg.executedWhy, s)
						continue
					}
					rec := groupCached[s.Package+"."+s.Symbol][round]
					extended, err := compartmentGrownRefresh(ctx, dir, unit, rec, checked)
					if err == nil && extended.Verdict.Status == gofresh.Valid {
						// The selected applicability is provisional until this
						// same view closes in revalidateServed. Only that path
						// returns the pair for installation.
						leg.chosen[s] = chosenWitness{index: extended.Index, solo: isSolo, record: rec}
						delete(wg.executedWhy, s)
						continue
					}
					// The primary (most recently installed) variant's refusal explains
					// the coming re-execution; fallback rounds fill in only when no
					// earlier round recorded one, and a later round's success deletes it.
					if round == 0 || wg.executedWhy[s] == (executedReason{}) {
						wg.executedWhy[s] = verdictExecutedReason(verdictReasonWithMovers(checked.Verdict.Reason, checked.MovedInputs))
						if err == nil && extended.Verdict.Reason != "" {
							wg.executedWhy[s] = verdictExecutedReason(verdictReasonWithMovers(extended.Verdict.Reason, extended.MovedInputs))
						}
					}
					if err != nil && ctx.Err() != nil {
						if abort, reason := classifyFault(err); !abort {
							return reason, nil
						}
						return "", err
					}
				}
				return "", nil
			}
			// Round-based variant checking: round N checks each unproven
			// subject's Nth variant, and the first variant proving equivalent
			// serves — deterministic by load order, most recently installed
			// first, so the first round tries the variant the last state
			// change produced. Variants
			// differing only in manifests or proof attachment can both prove
			// equivalent; each is a proven equivalence, so either serves
			// soundly. Rounds cost only fingerprint checks, never analysis or
			// execution.
			for round := 0; ; round++ {
				fps, advanced := roundCandidates(serving, groupCached, leg.chosen, round, declarations, func(s gofresh.Subject, why executedReason) {
					if round == 0 || wg.executedWhy[s] == (executedReason{}) {
						wg.executedWhy[s] = why
					}
				})
				if !advanced {
					break
				}
				if len(fps) == 0 {
					// Every variant this round was gate-refused; later
					// variants may still prove equivalent.
					continue
				}
				var batchCandidates, soloCandidates []gofresh.Candidate
				for _, s := range subjects {
					if fp, ok := fps[s]; ok {
						cand := gofresh.Candidate{Subject: s, Recorded: fp}
						if wg.view.isSolo(s) {
							soloCandidates = append(soloCandidates, cand)
						} else {
							batchCandidates = append(batchCandidates, cand)
						}
					}
				}
				if reason, err := checkRound(leg.checking, false, batchCandidates, round); reason != "" || err != nil {
					return nil, reason, err
				}
				if reason, err := checkRound(leg.soloChecking, true, soloCandidates, round); reason != "" || err != nil {
					return nil, reason, err
				}
			}
			for _, s := range subjects {
				if _, valid := leg.chosen[s]; valid {
					// Proven equivalent: the chosen variant serves, pending
					// post-run revalidation.
					wg.served = append(wg.served, s)
					continue
				}
				// Anything short of valid — stale, unverifiable, absent —
				// executes; absence of proof never serves an outcome. A
				// subject that fails to capture simply stays unpublishable;
				// its execution and evidence are untouched. A random-seeded
				// witness publishes nothing by contract, so it is never
				// fingerprinted.
				wg.stale[s.Package] = append(wg.stale[s.Package], s.Symbol)
			}
		}
		out = append(out, wg)
	}
	complete = true
	return out, "", nil
}

// servingCandidates partitions one group's subjects for the serving
// rounds: the subjects whose records may serve, with the group's own
// slice of the store (records this producer environment published,
// addressed by the test key). A subject serving refuses — a
// random-seeded witness, an unclassifiable one — never enters the
// rounds: its prior record, if one exists, is exactly the evidence the
// contract refuses to serve, so it executes, and when a record existed
// the refusal is attributed as its re-execution reason
// (REQ-evidence-witness-freshness-seeded).
func servingCandidates(groupID string, subjects []gofresh.Subject, neverServes map[gofresh.Subject]uncacheable, cached map[string][]witnesscache.Record, executedWhy map[gofresh.Subject]executedReason) ([]gofresh.Subject, map[string][]witnesscache.Record) {
	groupCached := map[string][]witnesscache.Record{}
	var serving []gofresh.Subject
	for _, s := range subjects {
		key := s.Package + "." + s.Symbol
		if why, refused := neverServes[s]; refused {
			if len(cached[groupID+"\x00"+key]) > 0 {
				executedWhy[s] = executedFromUncacheable(why)
			}
			continue
		}
		serving = append(serving, s)
		groupCached[key] = cached[groupID+"\x00"+key]
	}
	return serving, groupCached
}

// compartmentGrownRefresh adds an explicit native extension slot only for
// compartment staleness with a paired historical ledger. Its answer remains
// provisional until this checking unit closes; no endpoint installs here.
func compartmentGrownRefresh(ctx context.Context, dir string, unit *gofresh.CheckingUnit, rec witnesscache.Record, checked gofresh.CheckedCandidate) (gofresh.CheckedCandidate, error) {
	if checked.Verdict.Status != gofresh.Stale || checked.Verdict.Reason != gofresh.ReasonTestVariants {
		return checked, nil
	}
	if rec.CompartmentLedger == nil {
		rec.CompartmentLedger = witnesscache.LoadLedger(dir, rec)
	}
	if rec.CompartmentLedger == nil {
		return checked, nil
	}
	extended, err := unit.ExtendInertBatch(ctx, []gofresh.InertRequest{{Index: checked.Index, PriorLedger: rec.CompartmentLedger.ToGofresh()}})
	if err != nil {
		return checked, err
	}
	return extended[0], nil
}
