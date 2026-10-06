package golang

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/greatliontech/gofresh/closure"
	"github.com/greatliontech/gofresh/gotool"
	"github.com/greatliontech/stipulator/stipulate"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
)

// A policy invocation whose build selection moves standard-library
// surface bytes off every listed toolchain chain (netgo selects net's
// netgo files, which no chain row lists) runs under a selection the
// freshness engine's content-keyed toolchain-source audit refuses until
// its delta is walked: observation admissions strip and serving
// degrades to execution. The policy tier names that cost where the
// tags were declared — the notice carries the invocation name and the
// engine's own rendering — while admitted selections (a tag that
// constrains no standard-library file, the race selection of a listed
// toolchain) and non-witness invocations (which build no engine) raise
// nothing (REQ-check-policy-notices). The selection resolves under the
// invocation's own environment: a declared GOOS pin naming another
// platform raises the notice with no tag at all.
//
// Deliberately not //gofresh:pure: normalization shells the go toolchain.
func TestSelectionNoticesAttributeUnwalkedSelections(t *testing.T) {
	stipulate.Covers(t, "REQ-check-policy-notices")
	neutralAmbient(t)
	dir := discoverFixture(t)
	ambient, err := gotool.NormalizeEnv(os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	// The arms below are race selections: the gate asks the same, so a
	// host whose race selection cannot be listed (no cgo) skips rather
	// than reads its own "could not be read" notices as the audit's.
	if notice, err := closure.ToolchainSelectionNoticeResolved(context.Background(), gotool.NewEnvReader(ownedRunner, dir, ambient), selectionBuildFlags(true, nil)); err != nil || notice != "" {
		t.Skipf("the running toolchain's race selection is not admitted here (%v, %q); the version canary covers this", err, notice)
	}
	tagged := &stipulatorv1.GoInvocationConfig{}
	tagged.SetPackages([]string{"./..."})
	tagged.SetRace(true)
	tagged.SetTags([]string{"netgo"})
	vanilla := &stipulatorv1.GoInvocationConfig{}
	vanilla.SetPackages([]string{"./..."})
	vanilla.SetRace(true)
	plainTagged := &stipulatorv1.GoInvocationConfig{}
	plainTagged.SetPackages([]string{"./..."})
	plainTagged.SetTags([]string{"netgo"})
	admittedTag := &stipulatorv1.GoInvocationConfig{}
	admittedTag.SetPackages([]string{"./..."})
	admittedTag.SetRace(true)
	admittedTag.SetTags([]string{"dup"})
	// The selection is resolved under the invocation's OWN environment:
	// a declared GOOS pin naming another platform makes the race
	// selection unlistable (the go command refuses -race without cgo,
	// which a cross-platform build disables), which the audit refuses
	// naming the fault — with no tag at all, where the ambient
	// environment's host platform is admitted.
	platformPinned := &stipulatorv1.GoInvocationConfig{}
	platformPinned.SetPackages([]string{"./..."})
	platformPinned.SetRace(true)
	platformPinned.SetGoos("freebsd")
	pol := &stipulatorv1.TestPolicy{}
	pol.SetInvocations([]*stipulatorv1.PolicyInvocation{
		goInvocation("plain-tagged", plainTagged),
		goInvocation("tagged", tagged),
		goInvocation("vanilla", vanilla),
		goInvocation("admitted-tag", admittedTag),
		goInvocation("platform-pinned", platformPinned),
	})
	got := SelectionNotices(mustCapture(t, context.Background(), dir, pol))
	if len(got) != 2 {
		t.Fatalf("notices = %v, want exactly the two witness-eligible netgo invocations'", got)
	}
	for i, arm := range [][]string{{`invocation "tagged"`, " moved in "}, {`invocation "platform-pinned"`, " could not be read: "}} {
		for _, frag := range append(arm, "toolchain-selection audit: the audited surface of ", "observation admissions are disabled") {
			if !strings.Contains(got[i], frag) {
				t.Fatalf("notice %q missing %q", got[i], frag)
			}
		}
	}
	// Notices follow record order (REQ-core-determinism): several
	// tagged invocations raise theirs in the order the record lists them.
	var ordered []*stipulatorv1.PolicyInvocation
	var wantOrder []string
	for _, name := range []string{"tagged-d", "tagged-b", "tagged-a", "tagged-c"} {
		ordered = append(ordered, goInvocation(name, tagged))
		wantOrder = append(wantOrder, fmt.Sprintf("invocation %q", name))
	}
	pol.SetInvocations(ordered)
	got = SelectionNotices(mustCapture(t, context.Background(), dir, pol))
	if len(got) != len(wantOrder) {
		t.Fatalf("notices = %v, want one per tagged invocation", got)
	}
	for i, prefix := range wantOrder {
		if !strings.HasPrefix(got[i], prefix) {
			t.Fatalf("notice %d = %q, want record order %v", i, got[i], wantOrder)
		}
	}
}

// The verdict is the normalized form's: a form carrying a notice is
// reported under its name whatever environment it holds and a form
// carrying none is silent — the reader resolves nothing itself and
// judges nothing: that a non-witness invocation carries no verdict is
// the normalizer's rule, pinned by the plain-tagged arm above
// (REQ-check-policy-notices over REQ-check-derivation).
func TestSelectionNoticesReadTheFormsVerdicts(t *testing.T) {
	stipulate.Covers(t, "REQ-check-policy-notices")
	pc := &Capture{normalized: []*NormalizedInvocation{
		{Name: "noticed", Race: true, SelectionNotice: "toolchain-selection audit: synthetic", WitnessEnv: []string{"NOEQUALS"}},
		{Name: "admitted", Race: true},
	}}
	got := SelectionNotices(pc)
	if len(got) != 1 || got[0] != `invocation "noticed": toolchain-selection audit: synthetic` {
		t.Fatalf("notices = %v", got)
	}
}

// The one fault a resolution can meet past the taken snapshot is the
// run's own end: a context cancelled as the audit's listing spawns
// refuses the normalization naming the audit, as any spawn under an
// ended run would — a verdict is never stored admitted by default
// (REQ-check-policy-notices).
//
// Deliberately not //gofresh:pure: normalization shells the go toolchain.
func TestNormalizationRefusesWhenTheRunEndsMidAudit(t *testing.T) {
	stipulate.Covers(t, "REQ-check-policy-notices")
	neutralAmbient(t)
	dir := discoverFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	prior := commandHook
	commandHook = func(name string, args []string) {
		if name == "go" && len(args) > 0 && args[0] == "list" {
			cancel()
		}
	}
	t.Cleanup(func() { commandHook = prior })
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./..."})
	cfg.SetRace(true)
	// A tag of its own: a selection scope nothing under this test's
	// cache home has listed, so the audit must spawn its listing here
	// rather than serve gofresh's on-disk listing memo.
	cfg.SetTags([]string{"stipulatormidaudit"})
	_, err := NormalizeInvocation(ctx, dir, goInvocation("mid", cfg))
	if err == nil || !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "toolchain-source audit") {
		t.Fatalf("normalization under a run ended mid-audit: %v", err)
	}
}

// Normalization queries the environment exactly once: the audit's
// reader is primed with the snapshot effectiveGoEnv took, so the
// verdict costs no second `go env` — measured warm, after a
// normalization of the same selection listed its scope into gofresh's
// on-disk memo under this test's cache home, so the listing spawn is
// the memo's and the environment query stands alone
// (REQ-check-derivation).
//
// Deliberately not //gofresh:pure: normalization shells the go toolchain.
func TestNormalizationQueriesTheEnvironmentOnce(t *testing.T) {
	stipulate.Covers(t, "REQ-check-derivation")
	neutralAmbient(t)
	dir := discoverFixture(t)
	ctx := context.Background()
	c := countSpawns(t)
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./..."})
	cfg.SetRace(true)
	cfg.SetTags([]string{"stipulatoronequery"})
	inv := goInvocation("once", cfg)
	if _, err := NormalizeInvocation(ctx, dir, inv); err != nil {
		t.Fatal(err)
	}
	c.reset()
	if _, err := NormalizeInvocation(ctx, dir, inv); err != nil {
		t.Fatal(err)
	}
	if got := c.snapshot(); got.env != 1 || got.list != 0 {
		t.Fatalf("a warm normalization spawned %+v, want exactly one environment query and no listing", got)
	}
}
