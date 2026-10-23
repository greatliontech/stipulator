package golang

import (
	"slices"
	"strings"
	"testing"

	"github.com/greatliontech/gofresh/runtimeinput"
	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/internal/witnesscache"
	"github.com/greatliontech/stipulator/stipulate"
	"pgregory.net/rapid"
)

func checkReviewedSetProperties[T comparable](t *rapid.T, input, other []T, compare func(T, T) int) {
	original := slices.Clone(input)
	canonical := sortedCompact(input, compare)
	if !slices.Equal(input, original) {
		t.Fatal("canonicalization mutated input")
	}
	if !slices.Equal(canonical, sortedCompact(canonical, compare)) {
		t.Fatal("not idempotent")
	}
	permuted := slices.Clone(input)
	slices.Reverse(permuted)
	permuted = append(permuted, input...)
	if !slices.Equal(canonical, sortedCompact(permuted, compare)) {
		t.Fatal("permutation or duplicates changed the set")
	}
	set := map[T]bool{}
	for _, row := range input {
		set[row] = true
	}
	if len(canonical) != len(set) {
		t.Fatal("canonical set lost or retained duplicate rows")
	}
	for i, row := range canonical {
		if !set[row] || (i > 0 && compare(canonical[i-1], row) >= 0) {
			t.Fatal("canonical membership/order")
		}
	}
	want := true
	for _, row := range other {
		if !set[row] {
			want = false
		}
	}
	if got := containsCanonical(canonical, sortedCompact(other, compare), compare); got != want {
		t.Fatalf("containment = %v, map oracle = %v; current=%v recorded=%v", got, want, input, other)
	}
	if len(canonical) > 0 {
		canonical[0] = *new(T)
		if !slices.Equal(input, original) {
			t.Fatal("result aliases caller rows")
		}
	}
}

func TestReviewedRowsCanonicalProperties(t *testing.T) {
	t.Run("strings", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			rows := rapid.SliceOfN(rapid.StringMatching(`[abc./*]{0,8}`), 0, 30)
			checkReviewedSetProperties(t, rows.Draw(t, "current"), rows.Draw(t, "recorded"), strings.Compare)
		})
	})
	t.Run("namespace pairs", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			row := rapid.Custom(func(t *rapid.T) witnesscache.ScratchNamespace {
				return witnesscache.ScratchNamespace{Dir: rapid.SampledFrom([]string{".", "a", "b"}).Draw(t, "dir"), Pattern: rapid.SampledFrom([]string{"*", "a*", "b*", "a"}).Draw(t, "pattern")}
			})
			rows := rapid.SliceOfN(row, 0, 30)
			checkReviewedSetProperties(t, rows.Draw(t, "current"), rows.Draw(t, "recorded"), witnesscache.CompareScratchNamespaces)
		})
	})
}

func TestReviewedRowsComparatorDefinesIdentity(t *testing.T) {
	compare := func(a, b []string) int { return strings.Compare(a[0], b[0]) }
	rows := [][]string{{"b", "one"}, {"a", "two"}, {"b", "three"}}
	got := sortedCompact(rows, compare)
	if len(got) != 2 || got[0][0] != "a" || got[1][0] != "b" {
		t.Fatalf("comparator equality did not deduplicate non-comparable rows: %v", got)
	}
	if !containsCanonical(got, [][]string{{"b", "different"}}, compare) {
		t.Fatal("containment used structural equality instead of comparator equality")
	}
}

func TestReviewedDeclarationsRecordAndWithdraw(t *testing.T) {
	g := &captureGroup{excludedPaths: []string{"z", "a", "a"}, scratchNamespaces: []runtimeinput.ScratchNamespace{{Dir: "z", Pattern: "a*"}, {Dir: "a", Pattern: "z*"}, {Dir: "a", Pattern: "z*"}}}
	declarations := declarationsFor(g)
	g.excludedPaths[0] = "changed"
	g.scratchNamespaces[0].Dir = "changed"
	var rec witnesscache.Record
	declarations.recordInto(&rec)
	wantNS := []witnesscache.ScratchNamespace{{Dir: "a", Pattern: "z*"}, {Dir: "z", Pattern: "a*"}}
	if !slices.Equal(rec.ObservationExclusions, []string{"a", "z"}) || !slices.Equal(rec.ObservationNamespaces, wantNS) {
		t.Fatalf("recorded sets = %+v", rec)
	}
	if why := declarations.refusal(rec); why.String() != "" {
		t.Fatalf("own record refused: %s", why)
	}
	if why := declarations.refusal(witnesscache.Record{}); why.String() != "" {
		t.Fatalf("historical empty sets refused: %s", why)
	}
	var again witnesscache.Record
	declarations.recordInto(&again)
	rec.ObservationExclusions[0] = "mutated"
	rec.ObservationNamespaces[0].Dir = "mutated"
	if !slices.Equal(again.ObservationExclusions, []string{"a", "z"}) || !slices.Equal(again.ObservationNamespaces, wantNS) {
		t.Fatal("records alias one another")
	}
	var third witnesscache.Record
	declarations.recordInto(&third)
	if !slices.Equal(third.ObservationExclusions, again.ObservationExclusions) || !slices.Equal(third.ObservationNamespaces, again.ObservationNamespaces) {
		t.Fatal("record aliases declaration")
	}
	for _, tt := range []struct {
		name string
		g    *captureGroup
		why  string
	}{
		{"both withdrawn", &captureGroup{}, refusedWithdrawnExclusion},
		{"namespace withdrawn", &captureGroup{excludedPaths: []string{"a", "z"}}, refusedWithdrawnNamespace},
		{"exclusion withdrawn", &captureGroup{scratchNamespaces: []runtimeinput.ScratchNamespace{{Dir: "a", Pattern: "z*"}, {Dir: "z", Pattern: "a*"}}}, refusedWithdrawnExclusion},
		{"added rows", &captureGroup{excludedPaths: []string{"a", "b", "z"}, scratchNamespaces: []runtimeinput.ScratchNamespace{{Dir: "a", Pattern: "z*"}, {Dir: "z", Pattern: "a*"}, {Dir: "extra", Pattern: "*"}}}, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			d := declarationsFor(tt.g)
			if got := d.refusal(again).String(); got != tt.why {
				t.Fatalf("refusal = %q, want %q", got, tt.why)
			}
		})
	}
}

func TestReviewedDeclarationsExactRowsNotCoverage(t *testing.T) {
	d := declarationsFor(&captureGroup{excludedPaths: []string{"parent"}, scratchNamespaces: []runtimeinput.ScratchNamespace{{Dir: ".", Pattern: "*"}}})
	for _, rec := range []witnesscache.Record{
		{ObservationExclusions: []string{"parent/child"}},
		{ObservationNamespaces: []witnesscache.ScratchNamespace{{Dir: ".", Pattern: "run-*"}}},
	} {
		if d.refusal(rec) == (executedReason{}) {
			t.Fatalf("coverage substituted for exact identity: %+v", rec)
		}
	}
}

func TestReviewedDeclarationThirdTypeUsesSameCollection(t *testing.T) {
	// Registrations provide an isolated typed field for testing extensibility;
	// production registrations are not reviewed declarations.
	field := func(r *witnesscache.Record) *[]verify.Registration { return &r.Regs }
	compare := func(a, b verify.Registration) int { return strings.Compare(a.Requirement, b.Requirement) }
	rows := []verify.Registration{{Requirement: "R-b"}, {Requirement: "R-a"}}
	thirdReason := executedWithdrawnExclusion.with(" (third)")
	d := append(declarationsFor(&captureGroup{}), newRecordedSet(rows, compare, field, thirdReason))
	var rec witnesscache.Record
	d.recordInto(&rec)
	if len(rec.Regs) != 2 || rec.Regs[0].Requirement != "R-a" || d.refusal(rec) != (executedReason{}) {
		t.Fatal("third family did not record and gate")
	}
	withdrawn := append(declarationsFor(&captureGroup{}), newRecordedSet(rows[:1], compare, field, thirdReason))
	if why := withdrawn.refusal(rec); why != thirdReason {
		t.Fatalf("third-family refusal = %q", why)
	}
}

func TestReviewedNamespaceCanonicalAdapter(t *testing.T) {
	rows := []runtimeinput.ScratchNamespace{{Dir: "z", Pattern: "a"}, {Dir: "a", Pattern: "z"}, {Dir: "a", Pattern: "b"}}
	want := []witnesscache.ScratchNamespace{{Dir: "a", Pattern: "b"}, {Dir: "a", Pattern: "z"}, {Dir: "z", Pattern: "a"}}
	wantNative := []runtimeinput.ScratchNamespace{{Dir: "a", Pattern: "b"}, {Dir: "a", Pattern: "z"}, {Dir: "z", Pattern: "a"}}
	if got := canonicalReviewedNamespaces(rows); !slices.Equal(got, wantNative) {
		t.Fatalf("native namespace order drift: %+v", got)
	}
	if got := toRecordedNamespaces(canonicalReviewedNamespaces(rows)); !slices.Equal(got, want) {
		t.Fatalf("native/wire order drift: %+v", got)
	}
}

func TestReviewedCaptureKeyGoldenAndStableRecordIdentity(t *testing.T) {
	n := &NormalizedInvocation{WitnessEnv: []string{}, Race: true,
		ExcludedPaths: []string{"z", "a", "a"}, Vouches: []string{"dep.Var"},
		ScratchNamespaces: []runtimeinput.ScratchNamespace{{Dir: "scratch", Pattern: "run-*"}},
	}
	want := "tags=\x00env=\x00modulemode=\"GO_MODULE_MODE_UNSPECIFIED\"\x00module_root=\"\"\x00pgo=\"\"\x00args=\x00pure=false\x00race=true\x00exclusions=\"a\",\"z\"\x00vouches=\"dep.Var\"\x00namespaces=\"\\\"scratch\\\",\\\"run-*\\\"\""
	if got := groupKey(n); got != want {
		t.Fatalf("capture key bytes:\n got %q\nwant %q", got, want)
	}
	identity := groupIdentity(n)
	for _, change := range []func(*NormalizedInvocation){
		func(n *NormalizedInvocation) { n.ExcludedPaths = nil },
		func(n *NormalizedInvocation) { n.Vouches = nil },
		func(n *NormalizedInvocation) { n.ScratchNamespaces = nil },
	} {
		other := *n
		change(&other)
		if groupIdentity(&other) != identity {
			t.Fatal("declaration edit re-addressed records")
		}
		if groupKey(&other) == want {
			t.Fatal("declaration edit failed to partition capture")
		}
	}
}

func TestReviewedDeclarationsNeverUnionAcrossGroups(t *testing.T) {
	neutralAmbient(t)
	dir := discoverFixture(t)
	policy := &stipulatorv1.TestPolicy{}
	var invs []*stipulatorv1.PolicyInvocation
	for _, surface := range []string{"first", "second"} {
		cfg := &stipulatorv1.GoInvocationConfig{}
		cfg.SetPackages([]string{"./..."})
		cfg.SetPlainWitness(true)
		cfg.SetExcludedPaths([]string{surface})
		ns := &stipulatorv1.ScratchNamespace{}
		ns.SetDir("alpha")
		ns.SetPattern(surface + "-*")
		cfg.SetScratchNamespaces([]*stipulatorv1.ScratchNamespace{ns})
		invs = append(invs, goInvocation(surface, cfg))
	}
	policy.SetInvocations(invs)
	d, err := mustCapture(t, t.Context(), dir, policy).discover(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(d.groups) != 2 {
		t.Fatalf("groups = %d, want separate declaration sets", len(d.groups))
	}
	if d.groups[0].id != d.groups[1].id {
		t.Fatal("declarations re-addressed groups")
	}
	var records [2]witnesscache.Record
	for i, g := range d.groups {
		declarationsFor(g).recordInto(&records[i])
		if len(records[i].ObservationExclusions) != 1 || len(records[i].ObservationNamespaces) != 1 {
			t.Fatal("capture unioned declaration sets")
		}
	}
	for i, g := range d.groups {
		if declarationsFor(g).refusal(records[1-i]).String() != refusedWithdrawnExclusion {
			t.Fatal("another group's declaration licensed this group")
		}
		other := records[1-i]
		other.ObservationExclusions = nil
		if declarationsFor(g).refusal(other).String() != refusedWithdrawnNamespace {
			t.Fatal("another group's namespace licensed this group")
		}
	}
}

func TestReviewedDeclarationsPublishAndRestart(t *testing.T) {
	if testing.Short() {
		t.Skip("executes native producers and restarts captures")
	}
	stipulate.Covers(t, "REQ-evidence-witness-freshness-inputs", "REQ-evidence-witness-freshness-scratch-namespaces")
	neutralAmbient(t)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := writeModule(t, map[string]string{
		"go.mod": "module example.com/declarations\n\ngo 1.26\n",
		"p_test.go": `package p
import ("os"; "testing")
func TestRead(t *testing.T) { if os.Getenv("PWD") == "" { t.Fatal("missing PWD") } }
`,
	})
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"."})
	cfg.SetPlainWitness(true)
	cfg.SetExcludedPaths([]string{"bookkeeping"})
	ns := &stipulatorv1.ScratchNamespace{}
	ns.SetDir(".")
	ns.SetPattern("run-*")
	cfg.SetScratchNamespaces([]*stipulatorv1.ScratchNamespace{ns})
	policy := &stipulatorv1.TestPolicy{}
	policy.SetInvocations([]*stipulatorv1.PolicyInvocation{goInvocation("declarations", cfg)})
	writePolicyRecord(t, dir, policy)
	report, cold, err := ExecutePolicyWitnessed(t.Context(), mustCapture(t, t.Context(), dir, policy), noSeeding{})
	if err != nil || !SuiteHealthy(report) || cold.Ran != 1 || cold.Uncached != 0 {
		t.Fatalf("cold: %+v %v", cold, err)
	}
	records := witnesscache.Load(t.Context(), dir).Records
	if len(records) != 1 || !slices.Equal(records[0].ObservationExclusions, []string{"bookkeeping"}) || !slices.Equal(records[0].ObservationNamespaces, []witnesscache.ScratchNamespace{{Dir: ".", Pattern: "run-*"}}) {
		t.Fatalf("publication omitted reviewed provenance: %+v", records)
	}
	warm, err := RunWitnesses(t.Context(), dir, noSeeding{})
	if err != nil || warm.Fresh != 1 || warm.Ran != 0 {
		t.Fatalf("restart: %+v %v", warm, err)
	}
	cfg.SetScratchNamespaces(nil)
	writePolicyRecord(t, dir, policy)
	withdrawn, err := RunWitnesses(t.Context(), dir, noSeeding{})
	if err != nil || withdrawn.Fresh != 0 || withdrawn.Ran != 1 || withdrawn.ExecutedReasons["example.com/declarations.TestRead"] != refusedWithdrawnNamespace {
		t.Fatalf("namespace withdrawal: %+v %v", withdrawn, err)
	}
	cfg.SetExcludedPaths(nil)
	writePolicyRecord(t, dir, policy)
	withdrawn, err = RunWitnesses(t.Context(), dir, noSeeding{})
	if err != nil || withdrawn.Fresh != 0 || withdrawn.Ran != 1 || withdrawn.ExecutedReasons["example.com/declarations.TestRead"] != refusedWithdrawnExclusion {
		t.Fatalf("exclusion withdrawal: %+v %v", withdrawn, err)
	}
	// Both old declaration-bearing variants remain on disk. The new empty
	// variant must remain reachable and serve in a fresh capture.
	warm, err = RunWitnesses(t.Context(), dir, noSeeding{})
	if err != nil || warm.Fresh != 1 || warm.Ran != 0 {
		t.Fatalf("later declaration-free variant: %+v %v", warm, err)
	}
}
