package golang

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"testing"

	"pgregory.net/rapid"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/stipulate"
)

// TestGoPartitionConservationProperty quantifies partition conservation
// over generated universes and selections: every universe obligation
// appears in exactly one invocation's selection or is reported omitted;
// every obligation selected by more than one invocation is reported
// multiply selected with exactly its selectors; an obligation selected
// exactly once yields no finding.
func TestGoPartitionConservationProperty(t *testing.T) {
	stipulate.Covers(t, "REQ-policy-conservation")
	rapid.Check(t, func(t *rapid.T) {
		pool := make([]Obligation, 12)
		for i := range pool {
			pool[i] = Obligation{Kind: ObligationTest, Package: "example.com/p", Name: fmt.Sprintf("Test%c", 'A'+i)}
		}
		universeIdx := rapid.SliceOfNDistinct(rapid.IntRange(0, len(pool)-1), 0, len(pool), rapid.ID).Draw(t, "universe")
		var universe []Obligation
		for _, i := range universeIdx {
			universe = append(universe, pool[i])
		}
		nInv := rapid.IntRange(0, 4).Draw(t, "invocations")
		var selections []InvocationSelection
		selectedBy := map[string][]string{}
		for k := 0; k < nInv; k++ {
			name := fmt.Sprintf("inv%d", k)
			// Duplicates within one selection are legal caller input and must
			// count as a single selection by that invocation, never as
			// multiple selection.
			idx := rapid.SliceOfN(rapid.IntRange(0, len(pool)-1), 0, len(pool)+2).Draw(t, name)
			var obs []Obligation
			seenHere := map[string]bool{}
			for _, i := range idx {
				obs = append(obs, pool[i])
				if !seenHere[pool[i].ID()] {
					seenHere[pool[i].ID()] = true
					selectedBy[pool[i].ID()] = append(selectedBy[pool[i].ID()], name)
				}
			}
			selections = append(selections, InvocationSelection{Invocation: name, Obligations: obs})
		}
		reports := PartitionReports(universe, selections)
		byObligation := map[string]*stipulatorv1.ObligationReport{}
		for _, r := range reports {
			if byObligation[r.GetObligation()] != nil {
				t.Fatalf("obligation %q reported twice", r.GetObligation())
			}
			byObligation[r.GetObligation()] = r
		}
		inUniverse := map[string]bool{}
		for _, o := range universe {
			inUniverse[o.ID()] = true
		}
		// Oracle: walk the whole pool and check each obligation's expected
		// finding against the reports.
		expected := 0
		for _, o := range pool {
			id := o.ID()
			selectors := append([]string(nil), selectedBy[id]...)
			sort.Strings(selectors)
			r := byObligation[id]
			switch {
			case len(selectors) == 0 && inUniverse[id]:
				expected++
				if r == nil || r.GetDisposition() != stipulatorv1.ObligationDisposition_OBLIGATION_DISPOSITION_OMITTED {
					t.Fatalf("universe obligation %q selected by none: report = %v, want OMITTED", id, r)
				}
			case len(selectors) > 1:
				expected++
				if r == nil || r.GetDisposition() != stipulatorv1.ObligationDisposition_OBLIGATION_DISPOSITION_MULTIPLY_SELECTED {
					t.Fatalf("obligation %q selected by %v: report = %v, want MULTIPLY_SELECTED", id, selectors, r)
				}
				if !slices.Equal(r.GetInvocations(), selectors) {
					t.Fatalf("obligation %q reported selectors %v, want %v", id, r.GetInvocations(), selectors)
				}
			default:
				if r != nil {
					t.Fatalf("obligation %q (selected %d, universe=%v) spuriously reported: %v", id, len(selectors), inUniverse[id], r)
				}
			}
		}
		if len(reports) != expected {
			t.Fatalf("%d reports for %d expected findings", len(reports), expected)
		}
	})
}

// TestGoConservationReportWorkspace pins conservation end-to-end over the
// fixture workspace and real build selections: the derived complete
// policy partitions cleanly; dropping a member reports its obligations
// omitted; overlapping invocations report every shared obligation multiply
// selected; an invocation widening the build selection with tags selects
// its extra obligation without any spurious finding.
func TestGoConservationReportWorkspace(t *testing.T) {
	stipulate.Covers(t, "REQ-policy-conservation")
	stipulate.Covers(t, "REQ-go-workspace")
	neutralAmbient(t)
	dir := discoverFixture(t)
	ctx := context.Background()

	mkPolicy := func(invs ...*stipulatorv1.PolicyInvocation) *stipulatorv1.TestPolicy {
		p := &stipulatorv1.TestPolicy{}
		p.SetInvocations(invs)
		return p
	}
	rootCfg := func() *stipulatorv1.GoInvocationConfig {
		c := &stipulatorv1.GoInvocationConfig{}
		c.SetPackages([]string{"./..."})
		c.SetRace(true)
		return c
	}
	subCfg := func() *stipulatorv1.GoInvocationConfig {
		c := &stipulatorv1.GoInvocationConfig{}
		c.SetModuleRoot("sub")
		c.SetPackages([]string{"./..."})
		c.SetRace(true)
		return c
	}

	t.Run("complete policy partitions cleanly", func(t *testing.T) {
		derived, err := DerivePolicy(dir)
		if err != nil {
			t.Fatal(err)
		}
		reports, err := ConservationReport(ctx, dir, derived)
		if err != nil {
			t.Fatal(err)
		}
		if len(reports) != 0 {
			t.Errorf("complete policy yields findings: %v", reports)
		}
	})

	t.Run("omitted member reported", func(t *testing.T) {
		reports, err := ConservationReport(ctx, dir, mkPolicy(goInvocation("race", rootCfg())))
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, r := range reports {
			if r.GetDisposition() != stipulatorv1.ObligationDisposition_OBLIGATION_DISPOSITION_OMITTED {
				t.Errorf("unexpected disposition for %q: %v", r.GetObligation(), r.GetDisposition())
			}
			got = append(got, r.GetObligation())
		}
		want := []string{"package:example.com/sub", "test:example.com/sub.TestSub"}
		if !slices.Equal(got, want) {
			t.Errorf("omitted obligations = %q, want %q", got, want)
		}
	})

	t.Run("overlapping invocations reported", func(t *testing.T) {
		p := mkPolicy(
			goInvocation("race", rootCfg()),
			goInvocation("race-again", rootCfg()),
			goInvocation("race:sub", subCfg()),
		)
		reports, err := ConservationReport(ctx, dir, p)
		if err != nil {
			t.Fatal(err)
		}
		if len(reports) == 0 {
			t.Fatal("overlapping selections yielded no findings")
		}
		for _, r := range reports {
			if r.GetDisposition() != stipulatorv1.ObligationDisposition_OBLIGATION_DISPOSITION_MULTIPLY_SELECTED {
				t.Errorf("obligation %q: disposition %v, want MULTIPLY_SELECTED", r.GetObligation(), r.GetDisposition())
			}
			if !slices.Equal(r.GetInvocations(), []string{"race", "race-again"}) {
				t.Errorf("obligation %q selectors %v, want [race race-again]", r.GetObligation(), r.GetInvocations())
			}
		}
	})

	t.Run("build selection widening is not a finding", func(t *testing.T) {
		wide := rootCfg()
		wide.SetTags([]string{"special"})
		p := mkPolicy(goInvocation("race", wide), goInvocation("race:sub", subCfg()))
		reports, err := ConservationReport(ctx, dir, p)
		if err != nil {
			t.Fatal(err)
		}
		if len(reports) != 0 {
			t.Errorf("tag-widened complete policy yields findings: %v", reports)
		}
	})
}
