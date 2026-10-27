package golang

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"

	"github.com/greatliontech/gofresh"
	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/progress"
	"github.com/greatliontech/stipulator/internal/witnesscache"
)

type producerSubject struct {
	producer producerKey
	subject  gofresh.Subject
}

// retryResources owns execution storage through the last wave's judgments and
// their consumption. Package callbacks transfer only resources they own.
type retryResources struct{ release []func() }

// afterRetryWaveForTest observes actual rows and process accounts independently
// of the later witness-grant projection. It must not mutate the merge.
var afterRetryWaveForTest func(*execMerge)

func (r *retryResources) close() {
	for _, release := range r.release {
		release()
	}
}
func (r *retryResources) retain(unit packageUnit) {
	runs := append([]packageRun{unit.run}, unit.solos...)
	for _, run := range runs {
		if a := run.attempt; a != nil {
			if e := a.ownedEnvironment; e != nil {
				r.release = append(r.release, func() { _ = e.Close() })
				a.ownedEnvironment = nil
			}
			if path := a.logPath; path != "" {
				r.release = append(r.release, func() { _ = os.Remove(path) })
				a.logPath = ""
			}
		}
	}
}

func demonstratedDrift(err error) bool {
	var build *buildInputDrift
	var removed *removedRunnable
	return errors.Is(err, gofresh.ErrViewChanged) || errors.As(err, &build) || errors.As(err, &removed)
}

// removedRunnable is an observed absence in a complete current inventory, not
// a translation of an unknown-subject or construction error.
type removedRunnable struct{ subject gofresh.Subject }

func (e *removedRunnable) Error() string {
	return fmt.Sprintf("runnable %s.%s is absent from the observed current source generation", e.subject.Package, e.subject.Symbol)
}

func observedRunnableRemoval(population *runnableGeneration, subject gofresh.Subject) error {
	if population == nil {
		return nil
	}
	current, observed := population.packages[subject.Package]
	if !observed {
		return nil
	}
	for _, runnable := range current.runnables {
		if runnable.Name == subject.Symbol {
			return nil
		}
	}
	return &removedRunnable{subject: subject}
}

func observationDrift(o *ProcessObservation, subject gofresh.Subject) (uncacheable, bool) {
	if o == nil {
		return uncacheable{}, false
	}
	if movement, _ := nativeCloseCauses(o.closeErr); movement {
		return nativeCloseRefusal(o.closeErr), true
	}
	if demonstratedDrift(o.closeErr) {
		return reasonSourceFailed.with(o.closeErr.Error()), true
	}
	for _, result := range o.final.Subjects {
		if result.Subject == subject && result.Verdict.Status == gofresh.Stale {
			return reasonPostRun.with(result.Verdict.Reason), true
		}
	}
	return uncacheable{}, false
}

// retryCandidate is owned by an actual healthy retry process and top-level
// subject. Passing rows can grant witnesses; skipped rows can supply cache
// material but retain their skipped outcome. Native evidence is optional and
// never defines which actual outcomes require membership validation.
type retryCandidate struct {
	group  *witnessGroup
	key    producerSubject
	native *retryEvidence
}

// retryEvidence adds native freshness checking and optional deposit accounting
// to an actual candidate. Its absence grants no proof and erases no execution.
type retryEvidence struct {
	fingerprint gofresh.Fingerprint
	runtime     gofresh.RuntimeDescription
	reusable    bool
	record      *witnesscache.Record
}

func collectRetryCandidates(wg *witnessGroup, unit packageUnit, merge *execMerge, reasons map[gofresh.Subject]uncacheable) []*retryCandidate {
	var candidates []*retryCandidate
	for _, run := range append([]packageRun{unit.run}, unit.solos...) {
		if run.disposition != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_HEALTHY {
			continue
		}
		seen := map[producerSubject]bool{}
		for _, row := range run.tests {
			if row.GetOutcome() != stipulatorv1.TestOutcome_TEST_OUTCOME_PASSED && row.GetOutcome() != stipulatorv1.TestOutcome_TEST_OUTCOME_SKIPPED {
				continue
			}
			subject := gofresh.Subject{Package: row.GetPackage(), Symbol: topLevel(row.GetTest())}
			key := producerSubject{producer: keyOfProducer(row.GetProducer()), subject: subject}
			if seen[key] {
				continue
			}
			seen[key] = true
			candidate := &retryCandidate{group: wg, key: key}
			candidates = append(candidates, candidate)
			if reason, drifted := observationDrift(run.obs, subject); drifted {
				merge.denied[key] = reason
				reasons[subject] = reason
				continue
			}
			if run.obs == nil || run.obs.closeErr != nil {
				continue
			}
			for _, final := range run.obs.final.Subjects {
				if final.Subject == subject {
					candidate.native = &retryEvidence{fingerprint: final.Fingerprint, runtime: run.obs.runtime, reusable: final.Verdict.Status == gofresh.Valid}
					break
				}
			}
		}
	}
	return candidates
}

// settleRetries consumes each (capture group, subject) retry budget once. Every
// execution wave is followed by fresh checking transactions for surviving serves
// and previous retry evidence. Cyclic interference can revoke a grant, never
// replenish its execution budget or alter its raw process facts.
func settleRetries(ctx context.Context, pc *Capture, groups []*witnessGroup, pending map[*witnessGroup][]gofresh.Subject, merge *execMerge, resources *retryResources, install func([]witnesscache.Record) ([]witnesscache.Record, error), ordinal func() int32, scopeSkipped map[string]bool) ([]witnesscache.Record, map[gofresh.Subject]uncacheable, error) {
	used := map[*witnessGroup]map[gofresh.Subject]bool{}
	var candidates []*retryCandidate
	reasons := map[gofresh.Subject]uncacheable{}
	for {
		wave := map[*witnessGroup][]gofresh.Subject{}
		for _, wg := range groups {
			if used[wg] == nil {
				used[wg] = map[gofresh.Subject]bool{}
			}
			// Revalidation can introduce new drift after any wave. Invalid
			// serves have already been revoked; only permitted subjects may
			// consume retry work, regardless of which wave discovered them.
			for _, subject := range pc.execution.retrySubjects(pending[wg], scopeSkipped) {
				if !used[wg][subject] {
					used[wg][subject] = true
					wave[wg] = append(wave[wg], subject)
				}
			}
		}
		if len(wave) == 0 {
			break
		}
		current := newExecMerge()
		produced, why, err := retryDrifted(ctx, pc, groups, wave, current, resources, install, ordinal)
		if afterRetryWaveForTest != nil {
			afterRetryWaveForTest(current)
		}
		merge.absorb(current)
		for s, reason := range why {
			reasons[s] = reason
		}
		candidates = append(candidates, produced...)
		if err != nil {
			return nil, reasons, err
		}
		pending, err = recheckRetryWave(ctx, pc, groups, candidates, merge, reasons)
		if err != nil {
			return nil, reasons, err
		}
	}
	var records []witnesscache.Record
	// Independently closed deposits remain durable historical variants. Later
	// motion removes their current-run grant and reusable count, not their actual
	// producing history; a retry drifting before its deposit writes no record.
	for _, item := range candidates {
		if _, denied := merge.denied[item.key]; !denied && item.native != nil && item.native.reusable && item.native.record != nil {
			records = append(records, *item.native.record)
		}
	}
	return records, reasons, nil
}

type retryCheckEntry struct {
	subject  gofresh.Subject
	recorded gofresh.Fingerprint
	retry    *retryCandidate
}

// recheckRetryWave shares one new source/build generation per capture group.
// Construction failure is missing judgment, not evidence that an arbitrary
// already-executed subject drifted. Coherent population observations and typed
// build-input comparisons retain their own authority across that failure. Native
// transaction refusals apply only to their declared package/process subjects;
// ordinary initial passes are untouched.
func recheckRetryWave(ctx context.Context, pc *Capture, groups []*witnessGroup, candidates []*retryCandidate, merge *execMerge, reasons map[gofresh.Subject]uncacheable) (map[*witnessGroup][]gofresh.Subject, error) {
	pending := map[*witnessGroup][]gofresh.Subject{}
	for _, wg := range groups {
		var retries []*retryCandidate
		for _, item := range candidates {
			if item.group == wg {
				if _, denied := merge.denied[item.key]; !denied {
					retries = append(retries, item)
				}
			}
		}
		if len(wg.served) == 0 && len(retries) == 0 {
			continue
		}
		prepared, err := prepareWitnessGeneration(ctx, pc.dir, wg.g)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		invalid := map[gofresh.Subject]bool{}
		refuse := func(entry retryCheckEntry, verdict gofresh.Verdict, err error) {
			if entry.retry == nil {
				if err != nil || verdict.Status != gofresh.Valid {
					invalid[entry.subject] = true
					why := verdict.Reason
					if err != nil {
						why = err.Error()
					}
					wg.executedWhy[entry.subject] = executedRetryRevalidation.with(why)
				}
				return
			}
			reusable := err == nil && verdict.Status == gofresh.Valid
			if entry.retry.native != nil {
				entry.retry.native.reusable = reusable
			}
			if !reusable {
				why := verdict.Reason
				if err != nil {
					why = err.Error()
				}
				reasons[entry.subject] = reasonPostRun.with("retry evidence revalidation: " + why)
			}
			if demonstratedDrift(err) || err == nil && verdict.Status == gofresh.Stale {
				why := verdict.Reason
				if err != nil {
					why = err.Error()
				}
				reason := reasonPostRun.with("retry inputs changed: " + why)
				merge.denied[entry.retry.key] = reason
				reasons[entry.subject] = reason
			}
		}
		// Coherent population facts precede native capability. Native analysis
		// can fail, or have no roots at all, without erasing observed removals.
		for _, subject := range wg.served {
			if removal := observedRunnableRemoval(prepared.population, subject); removal != nil {
				refuse(retryCheckEntry{subject: subject}, gofresh.Verdict{}, removal)
			}
		}
		present := retries[:0]
		for _, item := range retries {
			if removal := observedRunnableRemoval(prepared.population, item.key.subject); removal != nil {
				refuse(retryCheckEntry{subject: item.key.subject, retry: item}, gofresh.Verdict{}, removal)
				continue
			}
			present = append(present, item)
		}
		retries = present
		if err != nil {
			for _, s := range wg.served {
				if !invalid[s] {
					refuse(retryCheckEntry{subject: s}, gofresh.Verdict{}, err)
				}
			}
			for _, item := range retries {
				// This comparison belongs to the group's shared compilation
				// inputs, so its demonstrated motion applies to every earlier
				// retry under that description. Generic construction failure
				// has no such subject attribution.
				var buildDrift *buildInputDrift
				if errors.As(err, &buildDrift) {
					refuse(retryCheckEntry{subject: item.key.subject, retry: item}, gofresh.Verdict{}, err)
				} else {
					if item.native != nil {
						item.native.reusable = false
					}
					reasons[item.key.subject] = reasonNoCapture.with("retry revalidation: " + err.Error())
				}
			}
		} else {
			check := func(runtime gofresh.RuntimeDescription, entries []retryCheckEntry) error {
				checkSlice := func(snap *gofresh.Snapshot, subEntries []retryCheckEntry) error {
					if len(subEntries) == 0 {
						return nil
					}
					var subjects []gofresh.Subject
					var candidates []gofresh.Candidate
					for _, entry := range subEntries {
						subjects = append(subjects, entry.subject)
						candidates = append(candidates, gofresh.Candidate{Subject: entry.subject, Recorded: entry.recorded})
					}
					binding, err := snap.BindRuntime(ctx, runtime)
					var results []gofresh.CheckedCandidate
					if err == nil {
						var unit *gofresh.CheckingUnit
						unit, err = binding.BeginCheck(subjects)
						if err == nil {
							defer unit.Abort()
							_, err = unit.CheckBatch(ctx, candidates)
							if err == nil {
								results, err = unit.Close(ctx)
							}
						}
					}
					if err == nil {
						err = prepared.inputs.validate(ctx)
					}
					if ctx.Err() != nil {
						return ctx.Err()
					}
					for i, entry := range subEntries {
						verdict := gofresh.Verdict{}
						if err == nil {
							verdict = results[i].Verdict
						}
						refuse(entry, verdict, err)
					}
					return nil
				}
				var batchEntries, soloEntries []retryCheckEntry
				for _, entry := range entries {
					if prepared.solo != nil && prepared.soloSubjects[entry.subject] {
						soloEntries = append(soloEntries, entry)
					} else {
						batchEntries = append(batchEntries, entry)
					}
				}
				if err := checkSlice(prepared.native, batchEntries); err != nil {
					return err
				}
				return checkSlice(prepared.solo, soloEntries)
			}
			for _, pkg := range wg.packages() {
				var entries []retryCheckEntry
				for _, s := range wg.servedIn(pkg) {
					if !invalid[s] {
						entries = append(entries, retryCheckEntry{subject: s, recorded: wg.selectedRecord(s).Fingerprint})
					}
				}
				if len(entries) == 0 {
					continue
				}
				environment := wg.legs[pkg].environment
				if err := check(gofresh.RuntimeDescription{Directory: environment.Dir, Environment: environment.Env}, entries); err != nil {
					return nil, err
				}
			}
			// One checking transaction per actual retry process, retaining its
			// complete immutable runtime description and every selected slot.
			checked := map[producerKey]bool{}
			for _, item := range retries {
				if item.native == nil || checked[item.key.producer] {
					continue
				}
				checked[item.key.producer] = true
				var entries []retryCheckEntry
				for _, other := range retries {
					if other.native != nil && other.key.producer == item.key.producer {
						entries = append(entries, retryCheckEntry{subject: other.key.subject, recorded: other.native.fingerprint, retry: other})
					}
				}
				if err := check(item.native.runtime, entries); err != nil {
					return nil, err
				}
			}
		}
		wg.served = slices.DeleteFunc(wg.served, func(subject gofresh.Subject) bool {
			if invalid[subject] {
				pending[wg] = append(pending[wg], subject)
				return true
			}
			return false
		})
	}
	return pending, nil
}

func noteRetryPublication(ctx context.Context, invocation, pkg string, records []witnesscache.Record) {
	if len(records) != 0 {
		progress.FromContext(ctx).Persisted("drift retry "+invocation+" "+pkg, len(records))
	}
}
