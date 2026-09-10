package golang

import (
	gofresh "github.com/greatliontech/gofresh"
	"github.com/greatliontech/stipulator/internal/witnesscache"
)

// groupTracker is the one completion rule both execution forms drive:
// a witness group is complete — the moment its records may publish
// and install — when every invocation covering one of its EXECUTING
// packages has completed; a package two invocations select covers
// nothing (its records never publish), and a package the form does
// not execute (a served one on the selective form) waits on no
// invocation. The unit of persistence on every form is the group at
// its last covering invocation's completion (REQ-policy-cancellation).
type groupTracker struct {
	pending  map[*captureGroup]map[string]bool
	byInv    map[string][]*captureGroup
	finished map[*captureGroup]bool
}

// newGroupTracker indexes the groups' covering invocations: executing
// says whether the form executes the group's package this run.
func newGroupTracker(groups []*captureGroup, executing func(g *captureGroup, pkg string) bool) *groupTracker {
	t := emptyTracker()
	for _, g := range groups {
		for pkg := range g.tests {
			if g.ambiguous[pkg] || !executing(g, pkg) {
				continue
			}
			inv, ok := g.pkgInv[pkg]
			if !ok {
				continue
			}
			if t.pending[g] == nil {
				t.pending[g] = map[string]bool{}
			}
			if !t.pending[g][inv] {
				t.pending[g][inv] = true
				t.byInv[inv] = append(t.byInv[inv], g)
			}
		}
	}
	return t
}

// invocationDone marks the invocation complete and returns, in the
// groups' order of first registration, every group it was the last
// covering invocation of — each returned once, and marked finished.
func (t *groupTracker) invocationDone(name string) []*captureGroup {
	var ready []*captureGroup
	for _, g := range t.byInv[name] {
		delete(t.pending[g], name)
		if len(t.pending[g]) > 0 || t.finished[g] {
			continue
		}
		t.finished[g] = true
		ready = append(ready, g)
	}
	return ready
}

// emptyTracker tracks no group: the tracker a degraded recorder holds,
// so its completion hook finds one on every exit.
func emptyTracker() *groupTracker {
	return &groupTracker{pending: map[*captureGroup]map[string]bool{}, byInv: map[string][]*captureGroup{}, finished: map[*captureGroup]bool{}}
}

// everyPackage is the full form's executing predicate: the run
// executes every package of every group.
func everyPackage(*captureGroup, string) bool { return true }

// selectedStalePackages is the selective form's executing predicate: a
// group's package executes this run when the run's selection names a
// test of it under the package's covering invocation. The selection is
// built from the stale subjects the scope admits, so a named package
// is a stale one and a stale package the scope left out is unnamed —
// it executes nothing and covers nothing.
func selectedStalePackages(staleSel map[string]TestSelection) func(g *captureGroup, pkg string) bool {
	return func(g *captureGroup, pkg string) bool {
		return len(staleSel[g.pkgInv[pkg]][pkg]) > 0
	}
}

// finish marks a group finished outside the invocation path — the
// selective form's all-served groups at verification — and reports
// whether it was not yet.
func (t *groupTracker) finish(g *captureGroup) bool {
	if t.finished[g] {
		return false
	}
	t.finished[g] = true
	return true
}

// installRecords offers the records to the store and returns the ones
// that landed; a record the store refused is not cached, and its
// subject's reason names the store's fault — a filesystem remedy,
// never the evidence's. One install path for both forms, so the
// account and the store never disagree.
func installRecords(dir string, records []witnesscache.Record, reasons map[gofresh.Subject]string) []witnesscache.Record {
	var installed []witnesscache.Record
	for _, rec := range records {
		if err := witnesscache.Install(dir, rec); err != nil {
			reasons[gofresh.Subject{Package: rec.Package, Symbol: rec.Test}] = "the store refused the record: " + err.Error()
			continue
		}
		installed = append(installed, rec)
	}
	return installed
}
