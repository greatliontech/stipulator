package golang

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"

	gofresh "github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/runtimeinput"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/policy"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/internal/witnesscache"
	"github.com/greatliontech/stipulator/stipulate"
)

// writePolicyRecord commits a policy record at its fixed location under
// dir, so RunWitnesses exercises the real loading seam.
func writePolicyRecord(t *testing.T, dir string, p *stipulatorv1.TestPolicy) {
	t.Helper()
	raw, err := policy.Render(p)
	if err != nil {
		t.Fatal(err)
	}
	full := filepath.Join(dir, filepath.FromSlash(policy.Path))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestGoRunWitnessesRequiresPolicyRecord pins the no-fallback rule: the
// selective witness runner consumes the committed test policy through the
// shared loading seam and surfaces every load failure — a record problem
// and an operational read fault alike — instead of assuming a universal
// suite.
//
//gofresh:pure
func TestGoRunWitnessesRequiresPolicyRecord(t *testing.T) {
	// The witness store must never be the user's real one: an
	// un-overridden store poisons this package's own observation and
	// pollutes the host cache (t.Setenv forbids t.Parallel, which these
	// tests drop for hermeticity).
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	stipulate.Covers(t, "REQ-policy-explicit")
	tmp := writeModule(t, map[string]string{
		"go.mod": "module example.com/nopolicy\n\ngo 1.26\n",
	})
	if _, err := RunWitnesses(context.Background(), tmp); !errors.Is(err, policy.ErrRecord) {
		t.Errorf("missing policy record: err = %v, want policy.ErrRecord", err)
	}

	recordPath := filepath.Join(tmp, filepath.FromSlash(policy.Path))
	if err := os.MkdirAll(filepath.Dir(recordPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(recordPath, []byte("not a policy record\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := RunWitnesses(context.Background(), tmp); !errors.Is(err, policy.ErrRecord) {
		t.Errorf("invalid policy record: err = %v, want policy.ErrRecord", err)
	}

	if os.Geteuid() == 0 {
		t.Skip("running as root: file permissions cannot make the record unreadable")
	}
	if err := os.Chmod(recordPath, 0o000); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(recordPath, 0o644)
	_, err := RunWitnesses(context.Background(), tmp)
	if err == nil {
		t.Fatal("unreadable policy record did not fail the run")
	}
	if errors.Is(err, policy.ErrRecord) {
		t.Errorf("operational read fault classified as a record problem: %v", err)
	}
}

// disjointWitnessFixture is the module the serve/execute/outside
// partition tests run: one race-covered package, one package no
// invocation covers, one only a non-race invocation covers, and one two
// race invocations cover. The outside packages' tests write sentinel
// files, so an execution that should never happen is a visible artifact.
func disjointWitnessFixture(t *testing.T) string {
	t.Helper()
	return writeModule(t, map[string]string{
		"go.mod":         "module example.com/wit\n\ngo 1.26\n",
		"covered/one.go": "package covered\n\nfunc One() string { return \"one-v1\" }\n",
		"covered/covered_test.go": `package covered

import "testing"

//gofresh:pure
func TestOne(t *testing.T) {
	t.Log("stipulator:covers REQ-wit-probe")
	if One() == "" {
		t.Fatal("empty")
	}
}
`,
		"covered2/two.go": "package covered2\n\nfunc Two() string { return \"two-v1\" }\n",
		"covered2/covered2_test.go": `package covered2

import "testing"

//gofresh:pure
func TestTwo(t *testing.T) {
	if Two() == "" {
		t.Fatal("empty")
	}
}
`,
		"uncovered/uncovered_test.go": `package uncovered

import (
	"os"
	"testing"
)

func TestNone(t *testing.T) {
	_ = os.WriteFile("ran.sentinel", nil, 0o644)
}
`,
		"nonrace/nonrace_test.go": `package nonrace

import (
	"os"
	"testing"
)

func TestPlain(t *testing.T) {
	_ = os.WriteFile("ran.sentinel", nil, 0o644)
}
`,
		"twice/twice_test.go": `package twice

import (
	"os"
	"testing"
)

func TestTwice(t *testing.T) {
	_ = os.WriteFile("ran.sentinel", nil, 0o644)
}
`,
	})
}

// requireNoSentinels asserts that no outside-policy package's test ever
// executed — an executed test would have left its sentinel file behind —
// while the multiply-selected package's sentinel MUST exist: it executes
// every run under each covering race invocation.
func requireNoSentinels(t *testing.T, tmp string) {
	t.Helper()
	for _, pkg := range []string{"uncovered", "nonrace"} {
		if _, err := os.Stat(filepath.Join(tmp, pkg, "ran.sentinel")); !os.IsNotExist(err) {
			t.Errorf("a process ran for outside-policy package %s: %v", pkg, err)
		}
	}
	if _, err := os.Stat(filepath.Join(tmp, "twice", "ran.sentinel")); err != nil {
		t.Errorf("the multiply-selected package never executed: %v", err)
	}
}

// TestGoRunWitnessesServeExecuteOutsideDisjoint pins the runner's
// partition: every expected witness subject is served, executed, or
// outside the policy — exactly one of the three — on the cold run, the
// warm run, and a partial-stale run alike. Outside-policy subjects — a
// package covered by no invocation or only by a non-race invocation —
// neither serve nor execute and ride the result as a
// count; serving grants witness outcomes and registrations without any
// health judgment, and a stale subject re-executes while its still-valid
// sibling serves.
func TestGoRunWitnessesServeExecuteOutsideDisjoint(t *testing.T) {
	stipulate.Covers(t, "REQ-core-one-execution", "REQ-evidence-witness-freshness",
		"REQ-evidence-freshness-no-health")
	if testing.Short() {
		t.Skip("executes a race-instrumented selective run over a temporary module")
	}
	neutralAmbient(t)
	tmp := disjointWitnessFixture(t)
	race := &stipulatorv1.GoInvocationConfig{}
	race.SetPackages([]string{"./covered", "./covered2", "./twice"})
	race.SetRace(true)
	dup := &stipulatorv1.GoInvocationConfig{}
	dup.SetPackages([]string{"./twice"})
	dup.SetRace(true)
	plain := &stipulatorv1.GoInvocationConfig{}
	plain.SetPackages([]string{"./nonrace"})
	p := &stipulatorv1.TestPolicy{}
	p.SetInvocations([]*stipulatorv1.PolicyInvocation{
		goInvocation("a-race", race),
		goInvocation("b-dup", dup),
		goInvocation("c-plain", plain),
	})
	writePolicyRecord(t, tmp, p)

	// Expected subjects: covered.TestOne, covered2.TestTwo, the doubly
	// covered twice.TestTwice — which executes every run under each
	// covering race invocation and never serves or publishes — and the two
	// outside ones (TestNone uncovered, TestPlain non-race).
	const expectedTotal = 5
	requirePartition := func(phase string, tr *verify.TestRun, fresh, ran int) {
		t.Helper()
		if tr.Degraded != "" {
			t.Fatalf("%s: freshness path degraded: %s", phase, tr.Degraded)
		}
		if tr.Fresh != fresh || tr.Ran != ran || tr.OutsidePolicy != 2 {
			t.Errorf("%s: fresh=%d ran=%d outside=%d, want %d/%d/2",
				phase, tr.Fresh, tr.Ran, tr.OutsidePolicy, fresh, ran)
		}
		// The disjointness invariant: each expected subject counted
		// exactly once across served, executed, and outside-policy. The
		// count identity holds when every executed subject reaches a
		// terminal event, as here; a denied subject leaves every bucket
		// and its visibility is its package-keyed diagnostic (pinned by
		// the envelope-cutoff test).
		if tr.Fresh+tr.Ran+tr.OutsidePolicy != expectedTotal {
			t.Errorf("%s: fresh+ran+outside = %d, want %d: a subject was double-granted or dropped",
				phase, tr.Fresh+tr.Ran+tr.OutsidePolicy, expectedTotal)
		}
		for _, key := range []string{
			"example.com/wit/covered.TestOne",
			"example.com/wit/covered2.TestTwo",
			"example.com/wit/twice.TestTwice",
		} {
			if got := tr.Outcomes[key]; got != verify.TestPassed {
				t.Errorf("%s: %s = %v, want PASSED", phase, key, got)
			}
		}
		for _, key := range []string{
			"example.com/wit/uncovered.TestNone",
			"example.com/wit/nonrace.TestPlain",
		} {
			if got, ok := tr.Outcomes[key]; ok {
				t.Errorf("%s: outside-policy subject %s carries outcome %v", phase, key, got)
			}
			if !tr.OutsideSubjects[key] {
				t.Errorf("%s: outside-policy subject %s not marked in OutsideSubjects", phase, key)
			}
		}
		for key := range tr.Outcomes {
			if tr.OutsideSubjects[key] {
				t.Errorf("%s: subject %s both witnessed and outside the selection", phase, key)
			}
		}
		wantReg := verify.Registration{Package: "example.com/wit/covered", Test: "TestOne", Requirement: "REQ-wit-probe"}
		found := false
		for _, reg := range tr.Registrations {
			if reg == wantReg {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: registration missing: %+v", phase, tr.Registrations)
		}
		requireNoSentinels(t, tmp)
	}

	cold, err := RunWitnesses(context.Background(), tmp)
	if err != nil {
		t.Fatal(err)
	}
	requirePartition("cold", cold, 0, 3)
	if cold.Uncached != 1 {
		t.Errorf("cold: uncached=%d, want 1: the multiply-selected subject executes but cannot publish", cold.Uncached)
	}
	cache := witnesscache.Load(tmp)
	if len(cache) != 2 {
		t.Fatalf("cold run published %d records, want 2: %+v", len(cache), cache)
	}
	for _, unpublished := range []struct{ pkg, test string }{
		{"example.com/wit/uncovered", "TestNone"},
		{"example.com/wit/nonrace", "TestPlain"},
		{"example.com/wit/twice", "TestTwice"},
	} {
		if cacheRecord(t, cache, unpublished.pkg, unpublished.test) != nil {
			t.Errorf("unservable subject %s.%s published a record", unpublished.pkg, unpublished.test)
		}
	}

	warm, err := RunWitnesses(context.Background(), tmp)
	if err != nil {
		t.Fatal(err)
	}
	requirePartition("warm", warm, 2, 1)

	// Stale exactly one subject: the edit reaches TestOne's closure and
	// not TestTwo's, so only TestOne re-executes while its sibling serves.
	onePath := filepath.Join(tmp, "covered", "one.go")
	src, err := os.ReadFile(onePath)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(src), "one-v1", "one-v2", 1)
	if edited == string(src) {
		t.Fatal("fixture edit failed")
	}
	if err := os.WriteFile(onePath, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	staleRun, err := RunWitnesses(context.Background(), tmp)
	if err != nil {
		t.Fatal(err)
	}
	requirePartition("stale sibling", staleRun, 1, 2)
}

// TestGoRunWitnessesServeUnderInertSiblingAddition pins
// REQ-evidence-witness-freshness's inert-growth carve-out end to end:
// adding a sibling test beside a cached witness moves only the package's
// test-variant compartment, so the cached witness serves — the movement is
// an addition no unchanged declaration can observe — while the added test
// itself executes; the served record republishes under the current
// compartment, so the following run serves both plainly; and a non-inert
// compartment movement (an added init function in a test file) refuses the
// carve-out and re-executes the package.
func TestGoRunWitnessesServeUnderInertSiblingAddition(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	stipulate.Covers(t, "REQ-evidence-witness-freshness")
	if testing.Short() {
		t.Skip("executes a race-instrumented selective run over a temporary module")
	}
	neutralAmbient(t)
	tmp := writeModule(t, map[string]string{
		"go.mod":     "module example.com/grow\n\ngo 1.26\n",
		"pkg/pkg.go": "package pkg\n\nfunc Value() string { return \"v1\" }\n",
		"pkg/pkg_test.go": `package pkg

import "testing"

//gofresh:pure
func TestBase(t *testing.T) {
	if Value() == "" {
		t.Fatal("empty")
	}
}
`,
	})
	race := &stipulatorv1.GoInvocationConfig{}
	race.SetPackages([]string{"./pkg"})
	race.SetRace(true)
	p := &stipulatorv1.TestPolicy{}
	p.SetInvocations([]*stipulatorv1.PolicyInvocation{goInvocation("race", race)})
	writePolicyRecord(t, tmp, p)

	requireRun := func(phase string, fresh, ran int) *verify.TestRun {
		t.Helper()
		tr, err := RunWitnesses(context.Background(), tmp)
		if err != nil {
			t.Fatal(err)
		}
		if tr.Degraded != "" {
			t.Fatalf("%s: freshness path degraded: %s", phase, tr.Degraded)
		}
		if tr.Fresh != fresh || tr.Ran != ran {
			t.Fatalf("%s: fresh=%d ran=%d, want %d/%d", phase, tr.Fresh, tr.Ran, fresh, ran)
		}
		return tr
	}
	requireRun("cold", 0, 1)
	requireRun("warm", 1, 0)

	// An added sibling test in a new file is an inert compartment
	// movement: the cached witness serves while only the new test runs.
	if err := os.WriteFile(filepath.Join(tmp, "pkg", "more_test.go"), []byte(`package pkg

import "testing"

//gofresh:pure
func TestMore(t *testing.T) {
	if Value() != "v1" {
		t.Fatal(Value())
	}
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	grown := requireRun("inert sibling addition", 1, 1)
	if got := grown.Outcomes["example.com/grow/pkg.TestBase"]; got != verify.TestPassed {
		t.Fatalf("served witness outcome = %v, want PASSED", got)
	}
	// The served record republished under the current compartment: a
	// TestBase variant now carries the same test-variant closure the added
	// test's fresh record pinned, and the next run serves both without
	// re-proving the delta.
	moreRec := cacheRecord(t, witnesscache.Load(tmp), "example.com/grow/pkg", "TestMore")
	if moreRec == nil {
		t.Fatal("added test published no record")
	}
	refreshed := false
	for _, rec := range witnesscache.Load(tmp) {
		if rec.Test == "TestBase" && rec.Fingerprint.TestVariantClosure == moreRec.Fingerprint.TestVariantClosure {
			refreshed = true
		}
	}
	if !refreshed {
		t.Fatal("carve-out serve did not republish the record under the current compartment")
	}
	requireRun("regrown warm", 2, 0)

	// An added init function in a test file is not inert: the carve-out
	// refuses and the package re-executes.
	if err := os.WriteFile(filepath.Join(tmp, "pkg", "init_test.go"), []byte("package pkg\n\nfunc init() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	requireRun("non-inert addition", 0, 2)
}

// A pin moving BEHIND the compartment verdict must re-execute even when
// the compartment delta itself is inert: gofresh orders the compartment
// comparison before the runtime tier, so a moved runtime input hides
// behind the stale "test variants" reason, and the carve-out's batched
// re-check of the refreshed fingerprint is what catches the mover
// (REQ-evidence-witness-freshness — the carve-out completes the proof
// itself, never widens it).
func TestGoRunWitnessesRerunsWhenMoverHidesBehindInertCompartmentGrowth(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	stipulate.Covers(t, "REQ-evidence-witness-freshness")
	if testing.Short() {
		t.Skip("executes a race-instrumented selective run over a temporary module")
	}
	neutralAmbient(t)
	tmp := writeModule(t, map[string]string{
		"go.mod":          "module example.com/hidden\n\ngo 1.26\n",
		"pkg/fixture.txt": "state-v1\n",
		"pkg/pkg_test.go": `package pkg

import (
	"os"
	"testing"
)

func TestReadsFixture(t *testing.T) {
	b, err := os.ReadFile("fixture.txt")
	if err != nil {
		t.Fatal(err)
	}
	if len(b) == 0 {
		t.Fatal("empty fixture")
	}
}
`,
	})
	race := &stipulatorv1.GoInvocationConfig{}
	race.SetPackages([]string{"./pkg"})
	race.SetRace(true)
	p := &stipulatorv1.TestPolicy{}
	p.SetInvocations([]*stipulatorv1.PolicyInvocation{goInvocation("race", race)})
	writePolicyRecord(t, tmp, p)

	requireRun := func(phase string, fresh, ran int) {
		t.Helper()
		tr, err := RunWitnesses(context.Background(), tmp)
		if err != nil {
			t.Fatal(err)
		}
		if tr.Degraded != "" {
			t.Fatalf("%s: freshness path degraded: %s", phase, tr.Degraded)
		}
		if tr.Fresh != fresh || tr.Ran != ran {
			t.Fatalf("%s: fresh=%d ran=%d, want %d/%d", phase, tr.Fresh, tr.Ran, fresh, ran)
		}
	}
	requireRun("cold", 0, 1)
	requireRun("warm", 1, 0)

	// One edit adds an inert sibling test AND moves the recorded
	// fixture read: the verdict reads stale "test variants" (the
	// compartment tier precedes the runtime tier), the refresh gate
	// passes on the inert delta, and only the batched re-check sees the
	// moved runtime input — the fixture reader must re-execute, never
	// serve.
	if err := os.WriteFile(filepath.Join(tmp, "pkg", "more_test.go"), []byte(`package pkg

import "testing"

//gofresh:pure
func TestMore(t *testing.T) {}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "pkg", "fixture.txt"), []byte("state-v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tr, err := RunWitnesses(context.Background(), tmp)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Degraded != "" {
		t.Fatalf("mover behind inert growth: freshness path degraded: %s", tr.Degraded)
	}
	if tr.Fresh != 0 || tr.Ran != 2 {
		t.Fatalf("mover behind inert growth: fresh=%d ran=%d, want 0/2", tr.Fresh, tr.Ran)
	}
	// The refusal is the carve-out re-check's own — it names the moved
	// input the compartment verdict hid, never the post-run drift
	// discard a wrongly-accepted serve would fall back to.
	reason := tr.ExecutedReasons["example.com/hidden/pkg.TestReadsFixture"]
	if strings.HasPrefix(reason, "mid-run drift:") {
		t.Fatalf("the hidden mover was served and only caught post-run: %q", reason)
	}
	if !strings.Contains(reason, "fixture.txt") {
		t.Fatalf("the refusal does not name the hidden mover: %q", reason)
	}
}

// A source mover landing during execution is invisible to the deferred
// checks — they compare same-generation facts and their closing base
// observation rides validation — so the group's closing validation is
// the one gate that catches it: every served outcome of the group is
// discarded with the validation's reason and re-derived by the run's
// single retry (REQ-evidence-witness-freshness's post-run revalidation).
func TestGoRunWitnessesSourceMoverDiscardsServedGroup(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	stipulate.Covers(t, "REQ-evidence-witness-freshness")
	if testing.Short() {
		t.Skip("executes a race-instrumented selective run over a temporary module")
	}
	neutralAmbient(t)
	tmp := writeModule(t, map[string]string{
		"go.mod":        "module example.com/srcmover\n\ngo 1.26\n",
		"served/lib.go": "package served\n\nfunc V() int { return 1 }\n",
		"served/served_test.go": `package served

import "testing"

//gofresh:pure
func TestServed(t *testing.T) {
	if V() != 1 {
		t.Fail()
	}
}
`,
		"writer/writer.go": "package writer\n",
		"writer/writer_test.go": `package writer

import (
	"os"
	"strconv"
	"testing"
)

func TestWriter(t *testing.T) {
	n := 0
	if b, err := os.ReadFile("counter.txt"); err == nil {
		n, _ = strconv.Atoi(string(b))
	}
	n++
	if err := os.WriteFile("counter.txt", []byte(strconv.Itoa(n)), 0o644); err != nil {
		t.Fatal(err)
	}
	if n == 2 {
		src, err := os.ReadFile("../served/lib.go")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile("../served/lib.go", append(src, []byte("\n// moved mid-run\n")...), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
`,
	})
	race := &stipulatorv1.GoInvocationConfig{}
	race.SetPackages([]string{"./..."})
	race.SetRace(true)
	p := &stipulatorv1.TestPolicy{}
	p.SetInvocations([]*stipulatorv1.PolicyInvocation{goInvocation("race", race)})
	writePolicyRecord(t, tmp, p)

	first, err := RunWitnesses(context.Background(), tmp)
	if err != nil {
		t.Fatal(err)
	}
	if first.Degraded != "" {
		t.Fatalf("first run degraded: %s", first.Degraded)
	}
	if first.Ran != 2 || first.Fresh != 0 {
		t.Fatalf("first run: ran=%d fresh=%d, want both executed", first.Ran, first.Fresh)
	}

	// Second run: the pure test serves at prepare time; the writer (its
	// own runtime inputs moved, so it never serves) re-executes and
	// moves the served package's source mid-run. The deferred re-check
	// cannot see a source mover; the closing validation refuses, the
	// serve is discarded with the validation's reason, and the retry
	// re-derives the outcome — nothing stale ever stands.
	second, err := RunWitnesses(context.Background(), tmp)
	if err != nil {
		t.Fatal(err)
	}
	if second.Degraded != "" {
		t.Fatalf("second run degraded: %s", second.Degraded)
	}
	if second.Fresh != 0 || second.Ran != 2 {
		t.Fatalf("second run: fresh=%d ran=%d, want the discarded serve re-derived by the retry", second.Fresh, second.Ran)
	}
	reason := second.ExecutedReasons["example.com/srcmover/served.TestServed"]
	if !strings.Contains(reason, "source producer validation failed") {
		t.Fatalf("discarded serve's reason = %q, want the closing validation's refusal", reason)
	}
	if got := second.Outcomes["example.com/srcmover/served.TestServed"]; got != verify.TestPassed {
		t.Fatalf("retried subject outcome = %v, want PASSED from the retry's execution", got)
	}
}

// TestGoRunWitnessesBindReadsUnderDirectoryBracketRoot pins the
// documentation-corpus shape end to end: a test reading a repo-root data
// file through a parent traversal serves warm when the policy declares the
// data directory as a bracket root — the traversal resolves congruently
// (gofresh REQ-inputs-path-congruence), the read binds to the bracket
// instead of sealing out-of-bracket, and an edit of the bound file stales
// the witness, proving the read is pinned rather than excused. The
// reader is a solo runnable whose read results are deliberately unused:
// the observed proof is what lifts file-I/O closure conservatism
// (REQ-inputs-observation-disposition confines that lift to the proof),
// today's proof admits value-unused reads only — content-asserting
// readers stay uncacheable until the observed proof admits value-used
// reads — and proofs attach to solo processes.
func TestGoRunWitnessesBindReadsUnderDirectoryBracketRoot(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	stipulate.Covers(t, "REQ-evidence-witness-freshness")
	if testing.Short() {
		t.Skip("executes a race-instrumented selective run over a temporary module")
	}
	neutralAmbient(t)
	tmp := writeModule(t, map[string]string{
		"go.mod":             "module example.com/corpus\n\ngo 1.26\n",
		"docs/law/source.md": "the corpus text\n",
		"pkg/data.txt":       "local\n",
		"pkg/pkg.go":         "package pkg\n\nfunc Kind() string { return \"corpus\" }\n",
		"pkg/pkg_test.go": `package pkg

import (
	"os"
	"testing"
)

func TestReadsCorpus(t *testing.T) {
	data, _ := os.ReadFile("../docs/law/source.md")
	_ = data
	_ = Kind()
}
`,
	})
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./pkg"})
	cfg.SetRace(true)
	cfg.SetBracketPaths([]string{"docs/law"})
	p := &stipulatorv1.TestPolicy{}
	p.SetInvocations([]*stipulatorv1.PolicyInvocation{goInvocation("race", cfg)})
	writePolicyRecord(t, tmp, p)

	requireRun := func(phase string, fresh, ran, uncached int) {
		t.Helper()
		tr, err := RunWitnesses(context.Background(), tmp)
		if err != nil {
			t.Fatal(err)
		}
		if tr.Degraded != "" {
			t.Fatalf("%s: freshness path degraded: %s", phase, tr.Degraded)
		}
		if tr.Fresh != fresh || tr.Ran != ran || tr.Uncached != uncached {
			t.Fatalf("%s: fresh=%d ran=%d uncached=%d, want %d/%d/%d; uncacheable: %v", phase, tr.Fresh, tr.Ran, tr.Uncached, fresh, ran, uncached, tr.UncacheableReasons)
		}
	}
	requireRun("cold", 0, 1, 0)
	requireRun("warm", 1, 0, 0)

	// The bound file's content is a recorded runtime input: an edit stales
	// the witness and it re-executes with the moved input named,
	// republishing under the new digest.
	if err := os.WriteFile(filepath.Join(tmp, "docs", "law", "source.md"), []byte("the corpus text, amended\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tr, err := RunWitnesses(context.Background(), tmp)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Degraded != "" || tr.Fresh != 0 || tr.Ran != 1 || tr.Uncached != 0 {
		t.Fatalf("edited corpus: %+v", tr)
	}
	if why := tr.ExecutedReasons["example.com/corpus/pkg.TestReadsCorpus"]; !strings.Contains(why, "source.md") {
		t.Fatalf("edited-corpus re-execution does not name the moved input: %q", why)
	}
	requireRun("re-warm", 1, 0, 0)
}

// TestCompartmentGrownServeGates pins the carve-out's gate directly
// (REQ-evidence-witness-freshness): only the exact stale "test variants"
// verdict with a persisted, inert-diffing compartment ledger serves — any
// other stale reason, a missing ledger, or a non-inert delta refuses — and
// a serve refreshes only the compartment pin, never the closure the
// verdict certified.
func TestCompartmentGrownServeGates(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	stipulate.Covers(t, "REQ-evidence-witness-freshness")
	if testing.Short() {
		t.Skip("builds a gofresh view over a temporary module")
	}
	neutralAmbient(t)
	tmp := writeModule(t, map[string]string{
		"go.mod":          "module example.com/gate\n\ngo 1.26\n",
		"pkg/pkg.go":      "package pkg\n\nfunc V() int { return 1 }\n",
		"pkg/pkg_test.go": "package pkg\n\nimport \"testing\"\n\nfunc TestV(t *testing.T) {\n\tif V() != 1 {\n\t\tt.Fail()\n\t}\n}\n",
	})
	engine, err := gofresh.New(gofresh.WithDir(tmp))
	if err != nil {
		t.Fatal(err)
	}
	s := gofresh.Subject{Package: "example.com/gate/pkg", Symbol: "TestV"}
	view, err := engine.NewView(context.Background(), []gofresh.Subject{s}, tmp)
	if err != nil {
		t.Fatal(err)
	}
	fp, err := view.Capture(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	ledger, err := view.TestVariantLedger(s)
	if err != nil {
		t.Fatal(err)
	}
	rec := witnesscache.Record{
		Package: s.Package, Test: s.Symbol,
		Fingerprint:       witnesscache.FromGofresh(fp),
		CompartmentLedger: witnesscache.LedgerFromGofresh(ledger),
		Outcomes:          map[string]string{s.Package + "." + s.Symbol: "passed"},
	}
	variants := gofresh.Verdict{Status: gofresh.Stale, Reason: "test variants"}
	if _, _, ok := compartmentGrownRefresh(context.Background(), view, rec, gofresh.Verdict{Status: gofresh.Stale, Reason: "maximal closure"}, s); ok {
		t.Fatal("a non-compartment stale reason rode the carve-out")
	}
	ledgerless := rec
	ledgerless.CompartmentLedger = nil
	if _, _, ok := compartmentGrownRefresh(context.Background(), view, ledgerless, variants, s); ok {
		t.Fatal("a ledgerless record rode the carve-out")
	}
	tampered := rec
	tampered.CompartmentLedger = witnesscache.LedgerFromGofresh(ledger)
	if len(tampered.CompartmentLedger.Declarations) == 0 {
		t.Fatal("fixture ledger carries no declarations")
	}
	tampered.CompartmentLedger.Declarations[0].Hash = "ffffffffffffffffffffffffffffffff"
	if _, _, ok := compartmentGrownRefresh(context.Background(), view, tampered, variants, s); ok {
		t.Fatal("a changed declaration rode the carve-out")
	}
	// The compartment comparison precedes the environment tiers in
	// gofresh's ladder, so a moved guard can hide behind the "test
	// variants" reason: the refresh alone must NOT serve — the batched
	// re-check of the refreshed fingerprint is what catches the mover,
	// exactly as the production rounds compose it.
	guardMoved := rec
	guardMoved.Fingerprint.Toolchain = "go0.0-never"
	_, movedFP, ok := compartmentGrownRefresh(context.Background(), view, guardMoved, variants, s)
	if !ok {
		t.Fatal("the refresh half refused a gate-passing record; the re-check owns guard movement")
	}
	movedVerdicts, err := checkFingerprints(context.Background(), view, map[gofresh.Subject]gofresh.Fingerprint{s: movedFP})
	if err != nil {
		t.Fatal(err)
	}
	if movedVerdicts[s].Status == gofresh.Valid {
		t.Fatal("a moved toolchain hid behind the compartment verdict and re-checked valid")
	}
	served, servedFP, ok := compartmentGrownRefresh(context.Background(), view, rec, variants, s)
	if !ok {
		t.Fatal("an inert delta under the exact verdict refused")
	}
	servedVerdicts, err := checkFingerprints(context.Background(), view, map[gofresh.Subject]gofresh.Fingerprint{s: servedFP})
	if err != nil {
		t.Fatal(err)
	}
	if servedVerdicts[s].Status != gofresh.Valid {
		t.Fatalf("an inert delta's refreshed fingerprint re-checked %+v, want valid", servedVerdicts[s])
	}
	if served.Fingerprint.TestVariantClosure != rec.Fingerprint.TestVariantClosure ||
		served.Fingerprint.MaximalClosure != rec.Fingerprint.MaximalClosure ||
		served.CompartmentLedger == nil {
		t.Fatalf("refresh rewrote the wrong pins: %+v", served.Fingerprint)
	}
}

// TestGoRunWitnessesIsolatesDeniedOutcomes pins that the selective
// executor's isolation pass flows through the runner: a completed pass
// inside a red process and a test shadowed by a sibling's abort both gain
// real solo outcomes attributed to their own producing processes, the
// denying failures stand, and only subjects a healthy process granted
// publish records.
func TestGoRunWitnessesIsolatesDeniedOutcomes(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness", "REQ-policy-attribution")
	if testing.Short() {
		t.Skip("executes a race-instrumented selective run over a temporary module")
	}
	neutralAmbient(t)
	tmp := writeModule(t, map[string]string{
		"go.mod": "module example.com/iso\n\ngo 1.26\n",
		"red/red_test.go": `package red

import "testing"

func TestGreen(t *testing.T) {}

func TestRed(t *testing.T) {
	t.Fatal("deliberately red")
}
`,
		"boom/boom_test.go": `package boom

import "testing"

func TestBoom(t *testing.T) {
	panic("fixture panic")
}

func TestShadowed(t *testing.T) {}
`,
		"state/state_test.go": `package state

import (
	"os"
	"testing"
)

var ready bool

func TestSetup(t *testing.T) { ready = true }

func TestNeedsSetup(t *testing.T) {
	if !ready {
		os.Exit(3)
	}
}

func TestStateRed(t *testing.T) { t.Fail() }
`,
	})
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./..."})
	cfg.SetRace(true)
	p := &stipulatorv1.TestPolicy{}
	p.SetInvocations([]*stipulatorv1.PolicyInvocation{goInvocation("all", cfg)})
	writePolicyRecord(t, tmp, p)

	tr, err := RunWitnesses(context.Background(), tmp)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Degraded != "" {
		t.Fatalf("freshness path degraded: %s", tr.Degraded)
	}
	want := map[string]verify.TestOutcome{
		"example.com/iso/red.TestGreen":      verify.TestPassed,
		"example.com/iso/red.TestRed":        verify.TestFailed,
		"example.com/iso/boom.TestShadowed":  verify.TestPassed,
		"example.com/iso/boom.TestBoom":      verify.TestFailed,
		"example.com/iso/state.TestSetup":    verify.TestPassed,
		"example.com/iso/state.TestStateRed": verify.TestFailed,
	}
	for key, wantOutcome := range want {
		if got := tr.Outcomes[key]; got != wantOutcome {
			t.Errorf("%s = %v, want %v", key, got, wantOutcome)
		}
	}
	// A completed pass inside the red process whose solo re-run dies
	// before a terminal event grants nothing: a red process yields no
	// green evidence, and the isolated outcome is the only path back.
	if got, ok := tr.Outcomes["example.com/iso/state.TestNeedsSetup"]; ok {
		t.Errorf("state.TestNeedsSetup = %v, want no outcome: its only pass rode a red process", got)
	}
	// The solo-granted passes publish from their own healthy processes;
	// the failing tests — and the state package, whose closure reaches
	// os.Exit unverifiably — stay uncacheable.
	if tr.Fresh != 0 || tr.Ran != 7 || tr.Uncached != 5 {
		t.Errorf("fresh=%d ran=%d uncached=%d, want 0/7/5", tr.Fresh, tr.Ran, tr.Uncached)
	}
	cache := witnesscache.Load(tmp)
	if cacheRecord(t, cache, "example.com/iso/red", "TestGreen") == nil {
		t.Error("isolated green-in-red pass did not publish from its solo process")
	}
	if cacheRecord(t, cache, "example.com/iso/boom", "TestShadowed") == nil {
		t.Error("unshadowed pass did not publish from its solo process")
	}
	if cacheRecord(t, cache, "example.com/iso/red", "TestRed") != nil {
		t.Error("red test published a record")
	}
	if cacheRecord(t, cache, "example.com/iso/boom", "TestBoom") != nil {
		t.Error("aborting test published a record")
	}
}

// TestGoRunWitnessesServedDriftRetriesOnce pins post-run served-record
// revalidation: a record that checked valid before execution and whose
// observed input another package's execution then mutated mid-run has its
// served outcome discarded and its subject re-executed once within the
// same run, so the run's evidence never reports a serve the current tree
// disproves.
func TestGoRunWitnessesServedDriftRetriesOnce(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness")
	if testing.Short() {
		t.Skip("executes a race-instrumented selective run over a temporary module")
	}
	neutralAmbient(t)
	tmp := writeModule(t, map[string]string{
		"go.mod":          "module example.com/driftserve\n\ngo 1.26\n",
		"reader/data.txt": "v1\n",
		"reader/reader_test.go": `package reader

import (
	"os"
	"testing"
)

func TestReads(t *testing.T) {
	_, _ = os.ReadFile("data.txt")
}
`,
		"writer/trigger.txt": "no\n",
		"writer/writer_test.go": `package writer

import (
	"os"
	"strings"
	"testing"
)

//gofresh:pure
func TestWritesOnce(t *testing.T) {
	raw, err := os.ReadFile("trigger.txt")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(raw)) != "yes" {
		return
	}
	if err := os.WriteFile("../reader/data.txt", []byte("v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}
`,
	})
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./..."})
	cfg.SetRace(true)
	p := &stipulatorv1.TestPolicy{}
	p.SetInvocations([]*stipulatorv1.PolicyInvocation{goInvocation("all", cfg)})
	writePolicyRecord(t, tmp, p)

	cold, err := RunWitnesses(context.Background(), tmp)
	if err != nil {
		t.Fatal(err)
	}
	if cold.Degraded != "" {
		t.Fatalf("cold: freshness path degraded: %s", cold.Degraded)
	}
	if cold.Fresh != 0 || cold.Ran != 2 {
		t.Fatalf("cold: fresh=%d ran=%d, want 0/2", cold.Fresh, cold.Ran)
	}
	if cacheRecord(t, witnesscache.Load(tmp), "example.com/driftserve/reader", "TestReads") == nil {
		t.Fatal("cold run published no record for the reader; the drift would prove nothing")
	}

	// Arm the writer: its own runtime input moves, so it re-executes and
	// mutates the reader's observed fixture mid-run — after the reader's
	// record checked valid, before the post-run revalidation.
	if err := os.WriteFile(filepath.Join(tmp, "writer", "trigger.txt"), []byte("yes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	drift, err := RunWitnesses(context.Background(), tmp)
	if err != nil {
		t.Fatal(err)
	}
	if drift.Degraded != "" {
		t.Fatalf("drift: freshness path degraded: %s", drift.Degraded)
	}
	// The reader's serve is discarded and the subject re-executes once:
	// nothing serves, both packages carry executed outcomes.
	if drift.Fresh != 0 || drift.Ran != 2 {
		t.Errorf("drift: fresh=%d ran=%d, want 0/2: the drifted serve must be discarded and re-executed", drift.Fresh, drift.Ran)
	}
	for _, key := range []string{
		"example.com/driftserve/reader.TestReads",
		"example.com/driftserve/writer.TestWritesOnce",
	} {
		if got := drift.Outcomes[key]; got != verify.TestPassed {
			t.Errorf("drift: %s = %v, want PASSED", key, got)
		}
	}
	// The retried reader republishes against the settled tree, and the
	// purity-asserted writer republishes under its author's opt-in.
	if drift.Uncached != 0 {
		t.Errorf("drift: uncached=%d, want 0", drift.Uncached)
	}
	if cacheRecord(t, witnesscache.Load(tmp), "example.com/driftserve/reader", "TestReads") == nil {
		t.Error("retried reader did not republish against the settled tree")
	}

	// The settled tree serves the retried record beside the writer's.
	settled, err := RunWitnesses(context.Background(), tmp)
	if err != nil {
		t.Fatal(err)
	}
	if settled.Degraded != "" {
		t.Fatalf("settled: freshness path degraded: %s", settled.Degraded)
	}
	if settled.Fresh != 2 || settled.Ran != 0 {
		t.Errorf("settled: fresh=%d ran=%d, want 2/0", settled.Fresh, settled.Ran)
	}
	if got := settled.Outcomes["example.com/driftserve/reader.TestReads"]; got != verify.TestPassed {
		t.Errorf("settled: served reader outcome = %v, want PASSED", got)
	}
}

// TestGoRunWitnessesSelfMutatingInputStaysUncacheable pins the
// run-to-ingest window on the witness path: a test that mutates its own
// observed input mid-run — the mutation persisting past process exit —
// moves its process's pre-spawn observation bracket, so the observation
// seals unverifiable, the record is dropped and counted uncacheable, and
// nothing is ever served for the subject: it re-executes every run until
// the test stops moving its own inputs or its author asserts purity.
func TestGoRunWitnessesSelfMutatingInputStaysUncacheable(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness")
	if testing.Short() {
		t.Skip("executes a race-instrumented selective run over a temporary module")
	}
	neutralAmbient(t)
	tmp := writeModule(t, map[string]string{
		"go.mod":       "module example.com/selfmut\n\ngo 1.26\n",
		"mut/data.txt": "stable-bytes\n",
		"mut/mut_test.go": `package mut

import (
	"os"
	"testing"
)

// TestRewritesOwnInput rewrites the same bytes it read: content stays
// fixed, but the metadata moves inside the run-to-ingest window.
func TestRewritesOwnInput(t *testing.T) {
	raw, err := os.ReadFile("data.txt")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("data.txt", raw, 0o644); err != nil {
		t.Fatal(err)
	}
}
`,
	})
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./..."})
	cfg.SetRace(true)
	p := &stipulatorv1.TestPolicy{}
	p.SetInvocations([]*stipulatorv1.PolicyInvocation{goInvocation("all", cfg)})
	writePolicyRecord(t, tmp, p)

	for _, phase := range []string{"cold", "second"} {
		tr, err := RunWitnesses(context.Background(), tmp)
		if err != nil {
			t.Fatal(err)
		}
		if tr.Degraded != "" {
			t.Fatalf("%s: freshness path degraded: %s", phase, tr.Degraded)
		}
		// The evidence stands — witnessing never depends on caching — but
		// nothing serves and nothing publishes: the subject executed, its
		// record dropped, uncacheable as a visible count.
		if got := tr.Outcomes["example.com/selfmut/mut.TestRewritesOwnInput"]; got != verify.TestPassed {
			t.Errorf("%s: outcome = %v, want PASSED from real execution", phase, got)
		}
		if tr.Fresh != 0 || tr.Ran != 1 || tr.Uncached != 1 {
			t.Errorf("%s: fresh=%d ran=%d uncached=%d, want 0/1/1: a self-mutating input must neither serve nor publish",
				phase, tr.Fresh, tr.Ran, tr.Uncached)
		}
		if cacheRecord(t, witnesscache.Load(tmp), "example.com/selfmut/mut", "TestRewritesOwnInput") != nil {
			t.Errorf("%s: a moved-bracket observation published a record", phase)
		}
	}
}

// TestGoRunWitnessesDegradesToFullExecution pins the degrade rule: a
// fault on the freshness path — here an analysis view that cannot be
// built over a package whose test sources fail to load — serves nothing
// and executes every covered subject, with the fault named on the result
// and the existing cache left alone. The full witnessing run is the
// selective runner with an empty served set.
func TestGoRunWitnessesDegradesToFullExecution(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-freshness-degrade")
	if testing.Short() {
		t.Skip("executes a race-instrumented selective run over a temporary module")
	}
	neutralAmbient(t)
	tmp := writeModule(t, map[string]string{
		"go.mod": "module example.com/degrade\n\ngo 1.26\n",
		"fine/fine_test.go": `package fine

import "testing"

//gofresh:pure
func TestOK(t *testing.T) {}
`,
	})
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./..."})
	cfg.SetRace(true)
	p := &stipulatorv1.TestPolicy{}
	p.SetInvocations([]*stipulatorv1.PolicyInvocation{goInvocation("all", cfg)})
	writePolicyRecord(t, tmp, p)

	cold, err := RunWitnesses(context.Background(), tmp)
	if err != nil {
		t.Fatal(err)
	}
	if cold.Degraded != "" || cold.Ran != 1 {
		t.Fatalf("cold: degraded=%q ran=%d, want a clean single-subject run", cold.Degraded, cold.Ran)
	}
	if cacheRecord(t, witnesscache.Load(tmp), "example.com/degrade/fine", "TestOK") == nil {
		t.Fatal("cold run published no record; the degrade would prove nothing about serving")
	}

	// A new package whose second test file fails to parse: discovery still
	// enumerates the parseable test, but the analysis view over the group
	// cannot be built — a freshness-path fault.
	for path, content := range map[string]string{
		"broken/ok_test.go": `package broken

import "testing"

func TestFine(t *testing.T) {}
`,
		"broken/broken_test.go": "package broken\n\nfunc {\n",
	} {
		full := filepath.Join(tmp, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	degraded, err := RunWitnesses(context.Background(), tmp)
	if err != nil {
		t.Fatal(err)
	}
	if degraded.Degraded == "" {
		t.Fatal("freshness-path fault did not name a degraded reason")
	}
	if degraded.Fresh != 0 {
		t.Errorf("degraded run served %d records; a degraded run serves nothing", degraded.Fresh)
	}
	// The cached, still-valid subject re-executes rather than serving.
	if got := degraded.Outcomes["example.com/degrade/fine.TestOK"]; got != verify.TestPassed {
		t.Errorf("covered subject did not re-execute under degrade: %v", got)
	}
	// The unloadable package's execution is attempted and build-fails: no
	// outcome, and the build failure rides the result keyed by package —
	// the denied subject's visibility story.
	if got, ok := degraded.Outcomes["example.com/degrade/broken.TestFine"]; ok {
		t.Errorf("unbuildable package produced outcome %v", got)
	}
	if packageDiagOutput(degraded, "example.com/degrade/broken") == "" {
		t.Errorf("unbuildable package carries no package-level diagnostic row: %+v", degraded.Diagnostics)
	}
	// Even the degraded empty-served form is the serving class: the mark
	// is the runner's identity, not a success bit.
	if !degraded.SelectiveServing {
		t.Error("degraded selective run lost its serving-class mark")
	}
	if degraded.Uncached != degraded.Ran {
		t.Errorf("uncached=%d ran=%d, want every executed subject counted uncacheable", degraded.Uncached, degraded.Ran)
	}
	// The degraded path leaves the cache alone: the prior record survives.
	if cacheRecord(t, witnesscache.Load(tmp), "example.com/degrade/fine", "TestOK") == nil {
		t.Error("degraded run dropped the existing cache")
	}
}

// TestGoRunWitnessesSoloSelectiveProcessPublishesProof pins the
// per-process observation-proof rule on the selective path: proof
// eligibility keys on the selective process running exactly one selected
// top-level runnable, not on the package's whole test population. The
// first run reds the package process — the flaky test fails once, so its
// record is refused while its denied sibling publishes from its solo
// isolation process — and the second run's one-stale-test selective
// process in the same many-test package publishes with an attached
// observation-completeness proof while the sibling serves.
func TestGoRunWitnessesSoloSelectiveProcessPublishesProof(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness")
	if testing.Short() {
		t.Skip("executes a race-instrumented selective run over a temporary module")
	}
	neutralAmbient(t)
	tmp := writeModule(t, map[string]string{
		"go.mod":       "module example.com/proofy\n\ngo 1.26\n",
		"pkg/flag.txt": "fail\n",
		"pkg/pkg_test.go": `package pkg

import (
	"os"
	"testing"
)

func TestFlakyReads(t *testing.T) {
	raw, _ := os.ReadFile("flag.txt")
	if string(raw) == "fail\n" {
		t.Fail()
	}
}

//gofresh:pure
func TestStable(t *testing.T) {}
`,
	})
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./..."})
	cfg.SetRace(true)
	p := &stipulatorv1.TestPolicy{}
	p.SetInvocations([]*stipulatorv1.PolicyInvocation{goInvocation("all", cfg)})
	writePolicyRecord(t, tmp, p)

	red, err := RunWitnesses(context.Background(), tmp)
	if err != nil {
		t.Fatal(err)
	}
	if red.Degraded != "" {
		t.Fatalf("red: freshness path degraded: %s", red.Degraded)
	}
	if got := red.Outcomes["example.com/proofy/pkg.TestFlakyReads"]; got != verify.TestFailed {
		t.Fatalf("red: flaky outcome = %v, want FAILED", got)
	}
	if got := red.Outcomes["example.com/proofy/pkg.TestStable"]; got != verify.TestPassed {
		t.Fatalf("red: denied sibling = %v, want PASSED from its solo process", got)
	}
	cache := witnesscache.Load(tmp)
	if cacheRecord(t, cache, "example.com/proofy/pkg", "TestFlakyReads") != nil {
		t.Fatal("red run published a record for the failing test")
	}
	if cacheRecord(t, cache, "example.com/proofy/pkg", "TestStable") == nil {
		t.Fatal("denied sibling did not publish from its solo isolation process")
	}

	// Settle the flaky test's observed input: only the recordless test
	// re-executes, in a selective process of its own.
	if err := os.WriteFile(filepath.Join(tmp, "pkg", "flag.txt"), []byte("pass\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	solo, err := RunWitnesses(context.Background(), tmp)
	if err != nil {
		t.Fatal(err)
	}
	if solo.Degraded != "" {
		t.Fatalf("solo: freshness path degraded: %s", solo.Degraded)
	}
	if solo.Fresh != 1 || solo.Ran != 1 {
		t.Fatalf("solo: fresh=%d ran=%d, want the stable sibling served and the recordless test re-executed", solo.Fresh, solo.Ran)
	}
	if got := solo.Outcomes["example.com/proofy/pkg.TestFlakyReads"]; got != verify.TestPassed {
		t.Errorf("solo: flaky outcome = %v, want PASSED", got)
	}
	rec := cacheRecord(t, witnesscache.Load(tmp), "example.com/proofy/pkg", "TestFlakyReads")
	if rec == nil {
		t.Fatal("solo selective process published no record")
	}
	if rec.Fingerprint.ObservationProof == nil || rec.Fingerprint.ObservationAssertion == "" {
		t.Errorf("solo selective process record carries no observation proof: %+v", rec.Fingerprint)
	} else if rec.Fingerprint.ObservationProof.Package != rec.Package ||
		rec.Fingerprint.ObservationProof.Symbol != rec.Test ||
		!rec.Fingerprint.ObservationProof.Observable {
		t.Errorf("observation proof does not attest the record's own subject: %+v", rec.Fingerprint.ObservationProof)
	}
}

// TestGoRunWitnessesDeniesEnvelopeCutoffPasses pins the per-process
// evidence gate where the isolation pass cannot repair it: a completed
// pass inside a process the envelope cut off is denied — a TIMEOUT
// process yields no green evidence and expiry denies the solo re-run
// before it spawns — so the pass neither witnesses nor publishes.
func TestGoRunWitnessesDeniesEnvelopeCutoffPasses(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness", "REQ-policy-attribution")
	if testing.Short() {
		t.Skip("executes a race-instrumented selective run over a temporary module")
	}
	neutralAmbient(t)
	tmp := writeModule(t, map[string]string{
		"go.mod": "module example.com/cutoff\n\ngo 1.26\n",
		"cut/cut_test.go": `package cut

import (
	"testing"
	"time"
)

func TestQuick(t *testing.T) {}

func TestSleeps(t *testing.T) {
	time.Sleep(10 * time.Minute)
}
`,
	})
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./..."})
	cfg.SetRace(true)
	inv := goInvocation("all", cfg)
	// The reviewed envelope admits the build and the quick test, never the
	// sleeper: the process is cut off with the quick pass already parsed.
	inv.SetTimeout(durationpb.New(20 * time.Second))
	p := &stipulatorv1.TestPolicy{}
	p.SetInvocations([]*stipulatorv1.PolicyInvocation{inv})
	writePolicyRecord(t, tmp, p)

	tr, err := RunWitnesses(context.Background(), tmp)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Degraded != "" {
		t.Fatalf("freshness path degraded: %s", tr.Degraded)
	}
	for _, key := range []string{
		"example.com/cutoff/cut.TestQuick",
		"example.com/cutoff/cut.TestSleeps",
	} {
		if got, ok := tr.Outcomes[key]; ok {
			t.Errorf("%s = %v, want no outcome: a cut-off process grants nothing", key, got)
		}
	}
	// A denied subject appears in no bucket: nothing served, nothing
	// reached a terminal event, nothing is outside the policy.
	if tr.Fresh != 0 || tr.Ran != 0 || tr.OutsidePolicy != 0 {
		t.Errorf("fresh=%d ran=%d outside=%d, want 0/0/0: denied subjects belong to no bucket", tr.Fresh, tr.Ran, tr.OutsidePolicy)
	}
	// The denied subjects' visibility is their package-level diagnostic
	// row: the cutoff must be traceable from the result.
	if packageDiagOutput(tr, "example.com/cutoff/cut") == "" {
		t.Errorf("cut-off package carries no package-level diagnostic row: %+v", tr.Diagnostics)
	}
	if len(witnesscache.Load(tmp)) != 0 {
		t.Errorf("a cut-off process published records: %+v", witnesscache.Load(tmp))
	}
}

// TestGoRunWitnessesDegradesOnUniverseFault pins that a fault while
// discovering the tree's obligation universe degrades exactly as any
// other freshness-path fault: the run serves nothing and executes every
// subject the policy covers, with the universe fault named on the result
// — never an error, because selection needs only the policy's own
// discovery, and work the policy leaves outside witnessing stays outside
// degraded or not.
func TestGoRunWitnessesDegradesOnUniverseFault(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-freshness-degrade")
	if testing.Short() {
		t.Skip("executes a race-instrumented selective run over a temporary module")
	}
	neutralAmbient(t)
	tmp := writeModule(t, map[string]string{
		"go.work": "go 1.26\n\nuse .\n",
		"go.mod":  "module example.com/uni\n\ngo 1.26\n",
		"covered/covered_test.go": `package covered

import "testing"

//gofresh:pure
func TestOK(t *testing.T) {}
`,
	})
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./covered"})
	cfg.SetRace(true)
	p := &stipulatorv1.TestPolicy{}
	p.SetInvocations([]*stipulatorv1.PolicyInvocation{goInvocation("all", cfg)})
	writePolicyRecord(t, tmp, p)

	cold, err := RunWitnesses(context.Background(), tmp)
	if err != nil {
		t.Fatal(err)
	}
	if cold.Degraded != "" || cold.Ran != 1 {
		t.Fatalf("cold: degraded=%q ran=%d, want a clean single-subject run", cold.Degraded, cold.Ran)
	}
	if cacheRecord(t, witnesscache.Load(tmp), "example.com/uni/covered", "TestOK") == nil {
		t.Fatal("cold run published no record; the degrade would prove nothing about serving")
	}

	// A workspace member whose baseline discovery selects no packages
	// faults the universe walk while the policy's own selection stays
	// whole: the policy invocation never touches the new member.
	if err := os.WriteFile(filepath.Join(tmp, "go.work"), []byte("go 1.26\n\nuse (\n\t.\n\t./empty\n)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(tmp, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "empty", "go.mod"), []byte("module example.com/uni/empty\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	degraded, err := RunWitnesses(context.Background(), tmp)
	if err != nil {
		t.Fatalf("universe fault surfaced as an error instead of degrading: %v", err)
	}
	if degraded.Degraded == "" {
		t.Fatal("universe fault did not name a degraded reason")
	}
	if degraded.Fresh != 0 {
		t.Errorf("degraded run served %d records; a degraded run serves nothing", degraded.Fresh)
	}
	if got := degraded.Outcomes["example.com/uni/covered.TestOK"]; got != verify.TestPassed {
		t.Errorf("covered subject did not re-execute under degrade: %v", got)
	}
	if degraded.Uncached != degraded.Ran {
		t.Errorf("uncached=%d ran=%d, want every executed subject counted uncacheable", degraded.Uncached, degraded.Ran)
	}
	if cacheRecord(t, witnesscache.Load(tmp), "example.com/uni/covered", "TestOK") == nil {
		t.Error("degraded run dropped the existing cache")
	}
}

// TestGoRunWitnessesCancellationDiscardsRun pins the discard contract on
// the selective witness surface (REQ-policy-cancellation): a caller
// cancellation mid-execution discards the partial run whole — the
// selective executor returns no partial SelectionResult, the runner
// returns no partial TestRun, and the witness cache is left byte-for-byte
// untouched.
func TestGoRunWitnessesCancellationDiscardsRun(t *testing.T) {
	stipulate.Covers(t, "REQ-policy-cancellation")
	if testing.Short() {
		t.Skip("executes race-instrumented selective runs over temporary modules")
	}
	neutralAmbient(t)

	sleepyModule := func(module string) (string, string) {
		tmp := writeModule(t, map[string]string{
			"go.mod": "module " + module + "\n\ngo 1.26\n",
			"sleepy/sleepy_test.go": `package sleepy

import (
	"os"
	"testing"
	"time"
)

func TestSleeps(t *testing.T) {
	_ = os.WriteFile("started.sentinel", nil, 0o644)
	time.Sleep(10 * time.Minute)
}
`,
		})
		return tmp, filepath.Join(tmp, "sleepy", "started.sentinel")
	}
	// cancelOnSentinel fires the cancellation only once the test binary
	// is demonstrably executing, so the cancel always lands mid-run,
	// never before the spawn.
	cancelOnSentinel := func(ctx context.Context, cancel context.CancelFunc, sentinel string) {
		go func() {
			deadline := time.Now().Add(3 * time.Minute)
			for time.Now().Before(deadline) {
				if _, err := os.Stat(sentinel); err == nil {
					cancel()
					return
				}
				select {
				case <-ctx.Done():
					return
				case <-time.After(25 * time.Millisecond):
				}
			}
			cancel()
		}()
	}

	// The selective executor discards the partial selection result.
	selDir, selSentinel := sleepyModule("example.com/cancelsel")
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./sleepy"})
	cfg.SetRace(true)
	n, err := NormalizeInvocation(context.Background(), selDir, goInvocation("solo", cfg))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cancelOnSentinel(ctx, cancel, selSentinel)
	res, err := ExecuteSelection(ctx, n, TestSelection{"example.com/cancelsel/sleepy": {"TestSleeps"}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ExecuteSelection err = %v, want context.Canceled", err)
	}
	if res != nil {
		t.Fatalf("partial selection result escaped a cancelled execution: %+v", res)
	}

	// The runner discards identically and leaves the store untouched.
	runDir, runSentinel := sleepyModule("example.com/cancelrun")
	writeRacePolicy(t, runDir)
	rctx, rcancel := context.WithCancel(context.Background())
	defer rcancel()
	cancelOnSentinel(rctx, rcancel, runSentinel)
	tr, err := RunWitnesses(rctx, runDir)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("RunWitnesses err = %v, want context.Canceled", err)
	}
	if tr != nil {
		t.Fatalf("partial test run escaped a cancelled witnessing run: %+v", tr)
	}
	store, err := witnesscache.StoreDir(runDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(store); !os.IsNotExist(err) {
		t.Fatalf("cancelled run touched the witness store: %v", err)
	}
}

// TestGoRunWitnessesMultiplySelectedRunsEveryRaceLeg pins that a package
// two same-group race invocations select executes under each covering
// invocation from that invocation's own discovery. The hasty leg's
// one-second timeout cannot revoke the patient leg's healthy grant —
// exactly the health-judged form's worst-wins derivation, where a
// timeout is invocation health, not a witness outcome — but its
// diagnostic must surface: the second leg demonstrably ran.
func TestGoRunWitnessesMultiplySelectedRunsEveryRaceLeg(t *testing.T) {
	stipulate.Covers(t, "REQ-core-one-execution", "REQ-check-verdict")
	if testing.Short() {
		t.Skip("executes a race-instrumented selective run over a temporary module")
	}
	neutralAmbient(t)
	tmp := writeModule(t, map[string]string{
		"go.mod": "module example.com/multi\n\ngo 1.26\n",
		"pkg/pkg_test.go": `package pkg

import (
	"testing"
	"time"
)

func TestSlowEnough(t *testing.T) {
	time.Sleep(3 * time.Second)
}
`,
	})
	patient := &stipulatorv1.GoInvocationConfig{}
	patient.SetPackages([]string{"./pkg"})
	patient.SetRace(true)
	hasty := &stipulatorv1.GoInvocationConfig{}
	hasty.SetPackages([]string{"./pkg"})
	hasty.SetRace(true)
	p := &stipulatorv1.TestPolicy{}
	inv1 := goInvocation("a-patient", patient)
	inv2 := goInvocation("b-hasty", hasty)
	inv2.SetTimeout(durationpb.New(time.Second))
	p.SetInvocations([]*stipulatorv1.PolicyInvocation{inv1, inv2})
	writePolicyRecord(t, tmp, p)

	tr, err := RunWitnesses(context.Background(), tmp)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Degraded != "" {
		t.Fatalf("freshness path degraded: %s", tr.Degraded)
	}
	if got := tr.Outcomes["example.com/multi/pkg.TestSlowEnough"]; got != verify.TestPassed {
		t.Fatalf("multiply-selected outcome = %v, want PASSED from the patient leg (health-judged parity); outcomes=%v",
			got, tr.Outcomes)
	}
	var hastyDiag bool
	for _, d := range tr.Diagnostics {
		if d.GetPackage() == "example.com/multi/pkg" && d.GetInvocation() == "b-hasty" {
			hastyDiag = true
		}
	}
	if !hastyDiag {
		t.Fatalf("no diagnostic from the hasty leg: the second covering invocation never executed; diagnostics=%v", tr.Diagnostics)
	}
}

// TestGoRunWitnessesMultiplySelectedRunsNonRaceLeg pins that a non-race
// invocation co-selecting a package contributes its failures to the
// witness-evidence run — and only failures: the tagged red test exists
// solely in the non-race leg's discovery, and its failure must surface
// while race-blind passes grant nothing.
func TestGoRunWitnessesMultiplySelectedRunsNonRaceLeg(t *testing.T) {
	stipulate.Covers(t, "REQ-core-one-execution", "REQ-check-verdict")
	if testing.Short() {
		t.Skip("executes a race-instrumented selective run over a temporary module")
	}
	neutralAmbient(t)
	tmp := writeModule(t, map[string]string{
		"go.mod":            "module example.com/multinr\n\ngo 1.26\n",
		"pkg/green_test.go": "package pkg\n\nimport \"testing\"\n\nfunc TestGreen(t *testing.T) {}\n",
		"pkg/red_test.go": `//go:build redflag

package pkg

import "testing"

func TestRedFlag(t *testing.T) {
	t.Fatal("only the non-race leg discovers me")
}

func TestGreenReg(t *testing.T) {
	t.Log("stipulator:covers REQ-nonrace-reg")
}
`,
	})
	race := &stipulatorv1.GoInvocationConfig{}
	race.SetPackages([]string{"./pkg"})
	race.SetRace(true)
	tagged := &stipulatorv1.GoInvocationConfig{}
	tagged.SetPackages([]string{"./pkg"})
	tagged.SetTags([]string{"redflag"})
	p := &stipulatorv1.TestPolicy{}
	p.SetInvocations([]*stipulatorv1.PolicyInvocation{
		goInvocation("a-race", race),
		goInvocation("b-tagged", tagged),
	})
	writePolicyRecord(t, tmp, p)

	tr, err := RunWitnesses(context.Background(), tmp)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Degraded != "" {
		t.Fatalf("freshness path degraded: %s", tr.Degraded)
	}
	if got := tr.Outcomes["example.com/multinr/pkg.TestRedFlag"]; got != verify.TestFailed {
		t.Fatalf("non-race leg failure = %v, want FAILED surfaced; outcomes=%v", got, tr.Outcomes)
	}
	if got := tr.Outcomes["example.com/multinr/pkg.TestGreen"]; got != verify.TestPassed {
		t.Fatalf("race-leg pass = %v, want PASSED from the race leg", got)
	}
	if got, ok := tr.Outcomes["example.com/multinr/pkg.TestGreenReg"]; ok {
		t.Fatalf("non-race pass granted an outcome %v; race rigor alone grants", got)
	}
	wantReg := verify.Registration{Package: "example.com/multinr/pkg", Test: "TestGreenReg", Requirement: "REQ-nonrace-reg"}
	var found bool
	for _, reg := range tr.Registrations {
		if reg == wantReg {
			found = true
		}
	}
	if !found {
		t.Fatalf("green non-race registration lost — the unbacked-registration cross-check is blind to it: %+v", tr.Registrations)
	}
}

// TestGoRunWitnessesServesAcrossTreeAlternation pins the variant store's
// point (REQ-evidence-witness-cache-format): two tree states of one test
// coexist as variants, so returning to an earlier state serves its
// witness instead of re-executing — branch ping-pong evicts nothing.
func TestGoRunWitnessesServesAcrossTreeAlternation(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-cache-format", "REQ-evidence-witness-freshness")
	if testing.Short() {
		t.Skip("executes a race-instrumented selective run over a temporary module")
	}
	neutralAmbient(t)
	tmp := writeModule(t, map[string]string{
		"go.mod":          "module example.com/alt\n\ngo 1.26\n",
		"pkg/lib.go":      "package pkg\n\nfunc State() string { return \"state-a\" }\n",
		"pkg/lib_test.go": "package pkg\n\nimport \"testing\"\n\n//gofresh:pure\nfunc TestState(t *testing.T) {\n\tif State() == \"\" {\n\t\tt.Fatal(\"empty\")\n\t}\n}\n",
	})
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./pkg"})
	cfg.SetRace(true)
	p := &stipulatorv1.TestPolicy{}
	p.SetInvocations([]*stipulatorv1.PolicyInvocation{goInvocation("race", cfg)})
	writePolicyRecord(t, tmp, p)

	stateA := "package pkg\n\nfunc State() string { return \"state-a\" }\n"
	stateB := "package pkg\n\nfunc State() string { return \"state-b\" }\n"
	libPath := filepath.Join(tmp, "pkg", "lib.go")

	run := func(phase string, wantFresh, wantRan int) {
		t.Helper()
		tr, err := RunWitnesses(context.Background(), tmp)
		if err != nil {
			t.Fatal(err)
		}
		if tr.Degraded != "" {
			t.Fatalf("%s: degraded: %s", phase, tr.Degraded)
		}
		if tr.Fresh != wantFresh || tr.Ran != wantRan {
			t.Fatalf("%s: fresh=%d ran=%d, want %d/%d", phase, tr.Fresh, tr.Ran, wantFresh, wantRan)
		}
		if wantRan == 0 && len(tr.ExecutedReasons) != 0 {
			t.Fatalf("%s: served run carries executed reasons %v - a refused earlier variant's reason must be deleted when a later variant serves", phase, tr.ExecutedReasons)
		}
	}
	run("cold state-a", 0, 1)
	if err := os.WriteFile(libPath, []byte(stateB), 0o644); err != nil {
		t.Fatal(err)
	}
	run("cold state-b", 0, 1)
	if err := os.WriteFile(libPath, []byte(stateA), 0o644); err != nil {
		t.Fatal(err)
	}
	run("returned to state-a", 1, 0)
	if err := os.WriteFile(libPath, []byte(stateB), 0o644); err != nil {
		t.Fatal(err)
	}
	run("returned to state-b", 1, 0)
}

// TestGoRunWitnessesToolchainReadStaysCacheable pins the guard-covered
// classification end to end: a witness reading the toolchain root
// publishes a cacheable record — the read is pinned by the toolchain
// guard, not sealed unverifiable — and serves on the next run.
func TestGoRunWitnessesToolchainReadStaysCacheable(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness")
	if testing.Short() {
		t.Skip("executes a race-instrumented selective run over a temporary module")
	}
	neutralAmbient(t)
	// A controlled module-cache root proves the module-cache leg of the
	// exemption deterministically: the fixture reads a file under it, and
	// the pinned frozen environment carries it to the spawned process.
	fakeModCache := filepath.Join(t.TempDir(), "modcache")
	if err := os.MkdirAll(filepath.Join(fakeModCache, "example.com"), 0o755); err != nil {
		t.Fatal(err)
	}
	pinnedMod := filepath.Join(fakeModCache, "example.com", "pinned.txt")
	if err := os.WriteFile(pinnedMod, []byte("immutable"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOMODCACHE", fakeModCache)
	tmp := writeModule(t, map[string]string{
		"go.mod": "module example.com/toolread\n\ngo 1.26\n",
		"pkg/pkg_test.go": `package pkg

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

//gofresh:pure
func TestReadsToolchain(t *testing.T) {
	if _, err := os.ReadFile(filepath.Join(runtime.GOROOT(), "VERSION")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.ReadFile(` + "`" + pinnedMod + "`" + `); err != nil {
		t.Fatal(err)
	}
}
`,
	})
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./pkg"})
	cfg.SetRace(true)
	p := &stipulatorv1.TestPolicy{}
	p.SetInvocations([]*stipulatorv1.PolicyInvocation{goInvocation("race", cfg)})
	writePolicyRecord(t, tmp, p)

	first, err := RunWitnesses(context.Background(), tmp)
	if err != nil {
		t.Fatal(err)
	}
	if first.Degraded != "" {
		t.Fatalf("degraded: %s", first.Degraded)
	}
	if first.Ran != 1 || first.Uncached != 0 {
		t.Fatalf("first run ran=%d uncached=%d, want 1/0: the toolchain read must not seal the record unverifiable", first.Ran, first.Uncached)
	}
	second, err := RunWitnesses(context.Background(), tmp)
	if err != nil {
		t.Fatal(err)
	}
	if second.Fresh != 1 || second.Ran != 0 {
		t.Fatalf("second run fresh=%d ran=%d, want 1/0: the guard-covered witness must serve", second.Fresh, second.Ran)
	}
	// The wiring's own claim: the toolchain read never entered the
	// manifest — guard-covered reads skip it entirely, they are not
	// merely tolerated as sealed unverifiable evidence.
	records := witnesscache.Load(tmp)
	if len(records) != 1 {
		t.Fatalf("store holds %d records, want 1", len(records))
	}
	desc, err := runtimeinput.Describe(records[0].Fingerprint.RuntimeInputs, tmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(desc.Paths) != 0 || len(desc.Unverifiable) != 0 {
		t.Fatalf("manifest carries paths=%v unverifiable=%v; the guard-covered read leaked into the manifest", desc.Paths, desc.Unverifiable)
	}
}

// TestGoRunWitnessesBuildCacheAndTempReadsStayCacheable pins the other
// two exemption classes end to end: a witness reading under the
// effective build cache is guard-covered, a witness stat'ing the temp
// root itself is admitted as ephemeral identity — neither seals the
// record nor enters the manifest, and the witness serves next run.
func TestGoRunWitnessesBuildCacheAndTempReadsStayCacheable(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness")
	if testing.Short() {
		t.Skip("executes a race-instrumented selective run over a temporary module")
	}
	neutralAmbient(t)
	// A controlled build cache proves the leg deterministically: the
	// fixture reads a file seeded under it, and the frozen environment
	// carries the root to both normalization and the spawned process.
	fakeGocache := filepath.Join(t.TempDir(), "gocache")
	if err := os.MkdirAll(fakeGocache, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fakeGocache, "pinned.txt"), []byte("derived"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOCACHE", fakeGocache)
	tempRoot := filepath.Join(t.TempDir(), "scratch")
	if err := os.MkdirAll(tempRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", tempRoot)
	tmp := writeModule(t, map[string]string{
		"go.mod": "module example.com/cacheread\n\ngo 1.26\n",
		"pkg/pkg_test.go": `package pkg

import (
	"os"
	"path/filepath"
	"testing"
)

// Reading a cache file as data is outside the admission's stated
// assumption — it is used here only as a deterministic vehicle for
// exercising the classification, not as an endorsed subject pattern.
//
//gofresh:pure
func TestReadsBuildCacheAndTempRoot(t *testing.T) {
	if _, err := os.ReadFile(filepath.Join(os.Getenv("GOCACHE"), "pinned.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(os.TempDir()); err != nil {
		t.Fatal(err)
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("PWD") != wd {
		t.Fatalf("PWD %q is not the spawn directory %q", os.Getenv("PWD"), wd)
	}
}
`,
	})
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./pkg"})
	cfg.SetRace(true)
	p := &stipulatorv1.TestPolicy{}
	p.SetInvocations([]*stipulatorv1.PolicyInvocation{goInvocation("race", cfg)})
	writePolicyRecord(t, tmp, p)

	first, err := RunWitnesses(context.Background(), tmp)
	if err != nil {
		t.Fatal(err)
	}
	if first.Degraded != "" {
		t.Fatalf("degraded: %s", first.Degraded)
	}
	if first.Ran != 1 || first.Uncached != 0 {
		t.Fatalf("first run ran=%d uncached=%d, want 1/0: the build-cache read and temp-root stat must not seal the record", first.Ran, first.Uncached)
	}
	if got := first.Outcomes["example.com/cacheread/pkg.TestReadsBuildCacheAndTempRoot"]; got != verify.TestPassed {
		t.Fatalf("witness outcome = %v, want PASSED — the fixture's own environment assertions failed", got)
	}
	second, err := RunWitnesses(context.Background(), tmp)
	if err != nil {
		t.Fatal(err)
	}
	if second.Fresh != 1 || second.Ran != 0 {
		t.Fatalf("second run fresh=%d ran=%d, want 1/0: the exempted witness must serve", second.Fresh, second.Ran)
	}
	records := witnesscache.Load(tmp)
	if len(records) != 1 {
		t.Fatalf("store holds %d records, want 1", len(records))
	}
	desc, err := runtimeinput.Describe(records[0].Fingerprint.RuntimeInputs, tmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(desc.Paths) != 0 || len(desc.Unverifiable) != 0 {
		t.Fatalf("manifest carries paths=%v unverifiable=%v; an exempted read leaked into the manifest", desc.Paths, desc.Unverifiable)
	}
}

// TestGoRunWitnessesExemptionBoundariesStayObserved pins the refusal
// side of the build-cache and temp-root exemptions: a read under the
// build cache's fuzz-corpus subtree and a read beneath the temp root
// (deeper than the root's own identity) stay observed — outside the
// package bracket they seal the observation, so an UNASSERTED witness
// executes but never publishes, with the sealing read named per test.
// (A purity-asserted subject would publish under the author's opt-in,
// REQ-purity-override — these fixtures are deliberately unasserted.)
func TestGoRunWitnessesExemptionBoundariesStayObserved(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness")
	if testing.Short() {
		t.Skip("executes a race-instrumented selective run over a temporary module")
	}
	neutralAmbient(t)
	fakeGocache := filepath.Join(t.TempDir(), "gocache")
	if err := os.MkdirAll(filepath.Join(fakeGocache, "fuzz"), 0o755); err != nil {
		t.Fatal(err)
	}
	corpus := filepath.Join(fakeGocache, "fuzz", "corpus.txt")
	if err := os.WriteFile(corpus, []byte("grown evidence"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOCACHE", fakeGocache)
	tempRoot := filepath.Join(t.TempDir(), "scratch")
	if err := os.MkdirAll(filepath.Join(tempRoot, "deep"), 0o755); err != nil {
		t.Fatal(err)
	}
	deep := filepath.Join(tempRoot, "deep", "data.txt")
	if err := os.WriteFile(deep, []byte("beneath the root"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", tempRoot)
	tmp := writeModule(t, map[string]string{
		"go.mod": "module example.com/boundary\n\ngo 1.26\n",
		"fuzzread/pkg_test.go": `package fuzzread

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadsFuzzCorpus(t *testing.T) {
	if _, err := os.ReadFile(filepath.Join(os.Getenv("GOCACHE"), "fuzz", "corpus.txt")); err != nil {
		t.Fatal(err)
	}
}
`,
		"tempread/pkg_test.go": `package tempread

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadsBeneathTempRoot(t *testing.T) {
	if _, err := os.ReadFile(filepath.Join(os.TempDir(), "deep", "data.txt")); err != nil {
		t.Fatal(err)
	}
}
`,
	})
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./..."})
	cfg.SetRace(true)
	p := &stipulatorv1.TestPolicy{}
	p.SetInvocations([]*stipulatorv1.PolicyInvocation{goInvocation("race", cfg)})
	writePolicyRecord(t, tmp, p)

	first, err := RunWitnesses(context.Background(), tmp)
	if err != nil {
		t.Fatal(err)
	}
	if first.Degraded != "" {
		t.Fatalf("degraded: %s", first.Degraded)
	}
	if first.Ran != 2 || first.Uncached != 2 {
		t.Fatalf("first run ran=%d uncached=%d, want 2/2: a sealed observation must not publish (reasons=%v)", first.Ran, first.Uncached, first.UncacheableReasons)
	}
	for test, sealing := range map[string]string{
		"example.com/boundary/fuzzread.TestReadsFuzzCorpus":      "corpus.txt",
		"example.com/boundary/tempread.TestReadsBeneathTempRoot": "data.txt",
	} {
		why, ok := first.UncacheableReasons[test]
		if !ok {
			t.Fatalf("uncacheable reasons = %v, missing %s", first.UncacheableReasons, test)
		}
		if !strings.Contains(why, sealing) {
			t.Errorf("reason %q for %s does not name the sealing read", why, test)
		}
	}
	if records := witnesscache.Load(tmp); len(records) != 0 {
		t.Fatalf("store holds %d sealed records, want none", len(records))
	}
	// Nothing published, so the second run re-executes — an
	// over-admitting classification would have published cacheable
	// records and served them here.
	second, err := RunWitnesses(context.Background(), tmp)
	if err != nil {
		t.Fatal(err)
	}
	if second.Fresh != 0 || second.Ran != 2 {
		t.Fatalf("second run fresh=%d ran=%d, want 0/2", second.Fresh, second.Ran)
	}
}

// TestGoRunWitnessesNamesUncacheableReasons pins the diagnosable-set
// requirement: an executed test whose record cannot publish carries the
// refusing leg's own reason on the run result — here the observation
// seal for a read outside the package's bracket root — keyed per test,
// so an unwarmable cache explains itself.
func TestGoRunWitnessesNamesUncacheableReasons(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness")
	if testing.Short() {
		t.Skip("executes a race-instrumented selective run over a temporary module")
	}
	neutralAmbient(t)
	tmp := writeModule(t, map[string]string{
		"go.mod":     "module example.com/whyfix\n\ngo 1.26\n",
		"shared.txt": "outside the package bracket\n",
		"pkg/pkg_test.go": `package pkg

import (
	"os"
	"testing"
)

func TestReadsOutsideBracket(t *testing.T) {
	if _, err := os.ReadFile("../shared.txt"); err != nil {
		t.Fatal(err)
	}
}
`,
	})
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./pkg"})
	cfg.SetRace(true)
	p := &stipulatorv1.TestPolicy{}
	p.SetInvocations([]*stipulatorv1.PolicyInvocation{goInvocation("race", cfg)})
	writePolicyRecord(t, tmp, p)

	tr, err := RunWitnesses(context.Background(), tmp)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Degraded != "" {
		t.Fatalf("degraded: %s", tr.Degraded)
	}
	if tr.Ran != 1 || tr.Uncached != 1 {
		t.Fatalf("ran=%d uncached=%d, want 1/1", tr.Ran, tr.Uncached)
	}
	why, ok := tr.UncacheableReasons["example.com/whyfix/pkg.TestReadsOutsideBracket"]
	if !ok {
		t.Fatalf("uncacheable reasons = %v, want the refusing leg named per test", tr.UncacheableReasons)
	}
	if !strings.Contains(why, "shared.txt") {
		t.Errorf("reason %q does not name the sealing input", why)
	}
}

// TestGoRunWitnessesAttributesDeniedAtZeroUncached pins the widened
// contract's unconditional arm: a subject denied execution outright is
// attributed even when every executed test published — the uncacheable
// count and the attribution map answer different questions.
func TestGoRunWitnessesAttributesDeniedAtZeroUncached(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness")
	if testing.Short() {
		t.Skip("executes a race-instrumented selective run over a temporary module")
	}
	neutralAmbient(t)
	tmp := writeModule(t, map[string]string{
		"go.mod":           "module example.com/denied\n\ngo 1.26\n",
		"ok/ok_test.go":    "package ok\n\nimport \"testing\"\n\n//gofresh:pure\nfunc TestOk(t *testing.T) {}\n",
		"denied/d_test.go": "package denied\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\nfunc TestMain(m *testing.M) { os.Exit(0) }\n\nfunc TestNeverRuns(t *testing.T) {}\n",
	})
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./..."})
	cfg.SetRace(true)
	p := &stipulatorv1.TestPolicy{}
	p.SetInvocations([]*stipulatorv1.PolicyInvocation{goInvocation("race", cfg)})
	writePolicyRecord(t, tmp, p)

	tr, err := RunWitnesses(context.Background(), tmp)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Degraded != "" {
		t.Fatalf("degraded: %s", tr.Degraded)
	}
	why, ok := tr.UncacheableReasons["example.com/denied/denied.TestNeverRuns"]
	if !ok {
		t.Fatalf("denied subject unattributed (uncached=%d reasons=%v)", tr.Uncached, tr.UncacheableReasons)
	}
	if !strings.Contains(why, "no process produced") && !strings.Contains(why, "terminal") {
		t.Errorf("denied reason = %q, want the denial leg named", why)
	}
}

// TestGoRunWitnessesNamesReExecutionReason pins executed-reason
// attribution: a subject with prior witness evidence that re-executes
// carries why serving refused it — for runtime-input drift, gofresh's
// mover attribution names the file.
func TestGoRunWitnessesNamesReExecutionReason(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness")
	if testing.Short() {
		t.Skip("executes a race-instrumented selective run over a temporary module")
	}
	neutralAmbient(t)
	tmp := writeModule(t, map[string]string{
		"go.mod":             "module example.com/whymove\n\ngo 1.26\n",
		"pkg/testdata/f.txt": "one",
		"pkg/pkg_test.go":    "package pkg\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\n//gofresh:pure\nfunc TestReads(t *testing.T) {\n\tif _, err := os.ReadFile(\"testdata/f.txt\"); err != nil {\n\t\tt.Fatal(err)\n\t}\n}\n",
	})
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./pkg"})
	cfg.SetRace(true)
	p := &stipulatorv1.TestPolicy{}
	p.SetInvocations([]*stipulatorv1.PolicyInvocation{goInvocation("race", cfg)})
	writePolicyRecord(t, tmp, p)

	first, err := RunWitnesses(context.Background(), tmp)
	if err != nil {
		t.Fatal(err)
	}
	if first.Uncached != 0 || len(first.ExecutedReasons) != 0 {
		t.Fatalf("cold run uncached=%d executedReasons=%v, want a clean publish with no prior evidence to attribute", first.Uncached, first.ExecutedReasons)
	}
	if err := os.WriteFile(filepath.Join(tmp, "pkg", "testdata", "f.txt"), []byte("two"), 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := RunWitnesses(context.Background(), tmp)
	if err != nil {
		t.Fatal(err)
	}
	if second.Ran != 1 {
		t.Fatalf("edited run ran=%d, want the stale subject re-executed", second.Ran)
	}
	why := second.ExecutedReasons["example.com/whymove/pkg.TestReads"]
	if !strings.Contains(why, "f.txt") {
		t.Fatalf("re-execution reason = %q (map %v), want the moved input named", why, second.ExecutedReasons)
	}
}

// A reviewed bracket path lets an unasserted witness consume a fixed
// external file cacheably: the pre-spawn bracket fingerprints it,
// observation binds the constant-path read, the proof stays closed, and
// the record serves until the file changes - while the identical
// witness without the declaration seals out-of-bracket. Process images
// follow the same mechanics but their spawning subjects additionally
// need a purity assertion, since child behavior is outside the testlog
// (REQ-evidence-witness-freshness).
func TestGoRunWitnessesBracketPathBindsExternalFile(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness")
	if testing.Short() {
		t.Skip("executes a race-instrumented selective run over a temporary module")
	}
	neutralAmbient(t)
	external := filepath.Join(t.TempDir(), "pinned.conf")
	if err := os.WriteFile(external, []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"go.mod": "module example.com/extread\n\ngo 1.26\n",
		"pkg/pkg_test.go": `package pkg

import (
	"os"
	"testing"
)

func TestReadsPinnedExternal(*testing.T) {
	_, _ = os.ReadFile("` + external + `")
}
`,
	}
	run := func(declare bool) *verify.TestRun {
		t.Helper()
		t.Setenv("XDG_CACHE_HOME", t.TempDir())
		tmp := writeModule(t, files)
		cfg := &stipulatorv1.GoInvocationConfig{}
		cfg.SetPackages([]string{"./pkg"})
		cfg.SetRace(true)
		if declare {
			cfg.SetBracketPaths([]string{external})
		}
		p := &stipulatorv1.TestPolicy{}
		p.SetInvocations([]*stipulatorv1.PolicyInvocation{goInvocation("race", cfg)})
		writePolicyRecord(t, tmp, p)
		first, err := RunWitnesses(context.Background(), tmp)
		if err != nil {
			t.Fatal(err)
		}
		if first.Degraded != "" {
			t.Fatalf("degraded: %s", first.Degraded)
		}
		if declare {
			if first.Ran != 1 || first.Uncached != 0 {
				t.Fatalf("declared first run ran=%d uncached=%d, want 1/0 (reasons=%v)", first.Ran, first.Uncached, first.UncacheableReasons)
			}
			second, err := RunWitnesses(context.Background(), tmp)
			if err != nil {
				t.Fatal(err)
			}
			if second.Fresh != 1 || second.Ran != 0 {
				t.Fatalf("declared second run fresh=%d ran=%d, want 1/0", second.Fresh, second.Ran)
			}
			// The bound external file's change stales the record.
			if err := os.WriteFile(external, []byte("v2"), 0o644); err != nil {
				t.Fatal(err)
			}
			third, err := RunWitnesses(context.Background(), tmp)
			if err != nil {
				t.Fatal(err)
			}
			if third.Fresh != 0 || third.Ran != 1 {
				t.Fatalf("declared third run fresh=%d ran=%d, want 0/1 after the bound file changed", third.Fresh, third.Ran)
			}
			if err := os.WriteFile(external, []byte("v1"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return first
	}

	run(true)
	bare := run(false)
	if bare.Uncached != 1 {
		t.Fatalf("undeclared first run uncached=%d, want 1 (reasons=%v)", bare.Uncached, bare.UncacheableReasons)
	}
}

// The invocation-wide reviewed purity assertion recovers reuse a
// mutated dynamic global would otherwise deny: without it the subject
// flags open-world and never publishes; with it the record publishes
// under the caller-assertion attribution and serves
// (REQ-purity-responsibility via the policy's assume_pure).
func TestGoRunWitnessesAssumePureRecoversOpenWorldSubjects(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness")
	if testing.Short() {
		t.Skip("executes a race-instrumented selective run over a temporary module")
	}
	neutralAmbient(t)
	files := map[string]string{
		"go.mod": "module example.com/openworld\n\ngo 1.26\n",
		"pkg/pkg.go": `package pkg

var hook = func() int { return 1 }

// Rebind makes hook a mutated-after-init dynamic global, the
// shared-dynamic-state downgrade's trigger.
func Rebind(f func() int) { hook = f }

func Value() int { return hook() }
`,
		"pkg/pkg_test.go": `package pkg

import "testing"

func TestValue(t *testing.T) {
	if Value() != 1 {
		t.Fatal("hook moved")
	}
}
`,
	}
	run := func(assume bool) (*verify.TestRun, *verify.TestRun) {
		t.Helper()
		t.Setenv("XDG_CACHE_HOME", t.TempDir())
		tmp := writeModule(t, files)
		cfg := &stipulatorv1.GoInvocationConfig{}
		cfg.SetPackages([]string{"./pkg"})
		cfg.SetRace(true)
		cfg.SetAssumePure(assume)
		p := &stipulatorv1.TestPolicy{}
		p.SetInvocations([]*stipulatorv1.PolicyInvocation{goInvocation("race", cfg)})
		writePolicyRecord(t, tmp, p)
		first, err := RunWitnesses(context.Background(), tmp)
		if err != nil {
			t.Fatal(err)
		}
		second, err := RunWitnesses(context.Background(), tmp)
		if err != nil {
			t.Fatal(err)
		}
		return first, second
	}

	first, second := run(true)
	if first.Ran != 1 || first.Uncached != 0 {
		t.Fatalf("assumed-pure first run ran=%d uncached=%d, want 1/0 (reasons=%v)", first.Ran, first.Uncached, first.UncacheableReasons)
	}
	if second.Fresh != 1 || second.Ran != 0 {
		t.Fatalf("assumed-pure second run fresh=%d ran=%d, want 1/0", second.Fresh, second.Ran)
	}

	bare, _ := run(false)
	if bare.Uncached != 1 {
		t.Fatalf("unasserted first run uncached=%d, want 1 (reasons=%v)", bare.Uncached, bare.UncacheableReasons)
	}
}

// packageDiagOutput returns the retained output of the package-level
// diagnostic row (one no single test owns) for pkg, or "" when none.
func packageDiagOutput(tr *verify.TestRun, pkg string) string {
	for _, d := range tr.Diagnostics {
		if d.GetTest() == "" && d.GetPackage() == pkg {
			return d.GetOutput()
		}
	}
	return ""
}

// A degraded freshness path under an id scope executes the scope's own
// full set, never the tree's: the scope gate keeps holding, and every
// out-of-scope in-policy subject records scope-skipped
// (REQ-evidence-freshness-degrade's scoped rule).
func TestGoRunWitnessesScopedDegradeExecutesScopeOnly(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-freshness-degrade")
	neutralAmbient(t)
	if testing.Short() {
		t.Skip("executes go test over a fixture module")
	}
	tmp := writeModule(t, map[string]string{
		"go.mod": "module example.com/scopedeg\n\ngo 1.26\n",
		"fine/fine_test.go": `package fine

import "testing"

func TestOK(t *testing.T) {}
`,
		"other/other_test.go": `package other

import "testing"

func TestOther(t *testing.T) {}
`,
		// The second file fails to parse: view construction faults, the
		// freshness path degrades.
		"broken/ok_test.go":     "package broken\n\nimport \"testing\"\n\nfunc TestFine(t *testing.T) {}\n",
		"broken/broken_test.go": "package broken\n\nfunc {\n",
	})
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./..."})
	cfg.SetRace(true)
	p := &stipulatorv1.TestPolicy{}
	p.SetInvocations([]*stipulatorv1.PolicyInvocation{goInvocation("all", cfg)})
	writePolicyRecord(t, tmp, p)

	scope := map[gofresh.Subject]bool{{Package: "example.com/scopedeg/fine", Symbol: "TestOK"}: true}
	tr, err := RunWitnessesScoped(context.Background(), tmp, p, scope)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Degraded == "" {
		t.Fatal("freshness-path fault did not degrade")
	}
	if got := tr.Outcomes["example.com/scopedeg/fine.TestOK"]; got != verify.TestPassed {
		t.Fatalf("in-scope subject = %v, want executed under the scoped degrade", got)
	}
	if _, ran := tr.Outcomes["example.com/scopedeg/other.TestOther"]; ran {
		t.Fatal("out-of-scope subject executed under the scoped degrade - the fallback was the tree's, not the scope's")
	}
	if !tr.ScopeSkipped["example.com/scopedeg/other.TestOther"] {
		t.Fatalf("out-of-scope subject not recorded scope-skipped: %v", tr.ScopeSkipped)
	}
}
