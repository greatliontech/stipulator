package coverage

import (
	"fmt"
	"maps"
	"reflect"
	"strings"
	"testing"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/compile"
	"github.com/greatliontech/stipulator/internal/proptest"
	"github.com/greatliontech/stipulator/internal/records"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/stipulate"
	"google.golang.org/protobuf/proto"
	"pgregory.net/rapid"
)

// propBackend resolves every symbol to one fixed shape, so generated
// bindings can be fully pinned and reach the static evidence tier.
type propBackend struct{ shape string }

func (b propBackend) Resolve(string) (verify.Resolution, string, error) {
	return verify.Resolved, b.shape, nil
}

// pipeline runs compile → verify → evaluate over one filesystem.
func pipeline(rt *rapid.T, files, extra map[string]string, backends map[string]verify.Backend) (*stipulatorv1.Spec, *verify.Report, *Report) {
	fsys := proptest.FS(files, extra)
	spec, diags, err := compile.Compile(fsys)
	if err != nil || len(diags) > 0 {
		rt.Fatalf("compile: %v %v", err, diags)
	}
	store, err := records.Load(fsys)
	if err != nil {
		rt.Fatal(err)
	}
	vr := verify.Run(spec, store, backends, nil)
	cr := Evaluate(spec, vr, store, false, nil)
	return spec, vr, cr
}

// TestPropPipelineDeterminism quantifies the determinism invariant:
// byte-identical inputs produce identical compile, verify, and coverage
// outputs on every run.
func TestPropPipelineDeterminism(t *testing.T) {
	stipulate.Covers(t, "REQ-core-determinism")
	rapid.Check(t, func(rt *rapid.T) {
		c := proptest.Gen(rt)
		files := c.Partition(rt, "p")

		// Records spread across several files, several per file: the
		// classic nondeterminism paths are directory iteration and
		// per-file accumulation order.
		extra := map[string]string{}
		for f := range rapid.IntRange(1, 3).Draw(rt, "bindFiles") {
			var b strings.Builder
			for range rapid.IntRange(1, 2).Draw(rt, "bindsPerFile") {
				b.WriteString(proptest.BindingText(rapid.SampledFrom(c.ReqIDs).Draw(rt, "bound"), ""))
			}
			extra[fmt.Sprintf(".stipulator/bindings/p%d.textproto", f)] = b.String()
		}
		for f := range rapid.IntRange(0, 2).Draw(rt, "gapFiles") {
			gapped := rapid.SampledFrom(c.ReqIDs).Draw(rt, "gapped")
			extra[fmt.Sprintf(".stipulator/gaps/p%d.textproto", f)] = proptest.GapText(gapped)
		}

		specA, vrA, crA := pipeline(rt, files, extra, nil)
		specB, vrB, crB := pipeline(rt, files, extra, nil)
		if !proto.Equal(specA, specB) {
			rt.Fatal("compile output differs across identical runs")
		}
		if !reflect.DeepEqual(vrA, vrB) {
			rt.Fatalf("verify output differs across identical runs:\n%+v\n---\n%+v", vrA, vrB)
		}
		if !reflect.DeepEqual(crA, crB) {
			rt.Fatalf("coverage output differs across identical runs:\n%+v\n---\n%+v", crA, crB)
		}
	})
}

// TestPropEvidenceOnlyFromCurrentRun quantifies evidence provenance: an
// unwitnessed run grants no witness-tier evidence regardless of what any
// record claims, and files outside the corpus and record stores — where
// any persisted result would live — never influence verification.
func TestPropEvidenceOnlyFromCurrentRun(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-promotion", "REQ-core-claims-untrusted")
	rapid.Check(t, func(rt *rapid.T) {
		// MUST-only requirements: every generated clause demands
		// witness-tier evidence or stronger, so nothing may read covered.
		c := proptest.Gen(rt, proptest.MustOnly())
		files := c.Partition(rt, "p")
		bound := rapid.SampledFrom(c.ReqIDs).Draw(rt, "bound")

		// The binding is fully pinned against a resolving backend: the
		// strongest static state a record can claim. Nothing weaker than
		// a current-run witness may cover a MUST clause, so even this
		// binding must not.
		spec0, diags, err := compile.Compile(proptest.FS(files, nil))
		if err != nil || len(diags) > 0 {
			rt.Fatalf("compile: %v %v", err, diags)
		}
		contentHash := ""
		for _, r := range spec0.GetRequirements() {
			if r.GetId() == bound {
				contentHash = r.GetContentHash()
			}
		}
		shape := strings.Repeat("s", 64)
		backends := map[string]verify.Backend{"go": propBackend{shape}}
		extra := map[string]string{
			".stipulator/bindings/p.textproto": proptest.BindingTextPinned(bound, contentHash, shape),
		}

		spec, _, cr := pipeline(rt, files, extra, backends)
		for _, r := range cr.Requirements {
			if r.Bucket == Covered {
				rt.Fatalf("%s covered without a current-run witness", r.Id)
			}
		}

		// A passed outcome claimed outside a witnessed run grants
		// nothing: evidence is born only in the current run.
		store, err := records.Load(proptest.FS(files, extra))
		if err != nil {
			rt.Fatal(err)
		}
		res := result(bound, tests, true, verify.Resolved, verify.ShapeMatch, verify.TestPassed)
		res.WitnessClass = verify.PropertyWitness
		unwitnessed := Evaluate(spec, &verify.Report{Results: []verify.BindingResult{res}}, store, false, nil)
		for _, r := range unwitnessed.Requirements {
			if r.Id == bound && r.Bucket == Covered {
				rt.Fatalf("%s covered by a claimed outcome outside a witnessed run", bound)
			}
		}

		// A would-be persisted result is never an input: junk files
		// outside the corpus and record globs change nothing.
		junk := map[string]string{
			".stipulator/report.textproto": "verdict: \"covered\"\n",
			".stipulator/cache/last.json":  `{"covered": true}`,
			"specs/report.txt":             "all requirements covered",
			"REPORT.md":                    "# All covered\n",
		}
		maps.Copy(junk, extra)
		_, vrClean, crClean := pipeline(rt, files, extra, backends)
		_, vrJunk, crJunk := pipeline(rt, files, junk, backends)
		if !reflect.DeepEqual(vrClean, vrJunk) {
			rt.Fatalf("non-record files changed verification:\n%+v\n---\n%+v", vrClean, vrJunk)
		}
		if !reflect.DeepEqual(crClean, crJunk) {
			rt.Fatalf("non-record files changed coverage:\n%+v\n---\n%+v", crClean, crJunk)
		}

		// Inside a record directory the same stray is a hard load error:
		// the boundary is loud in both directions, never a silent read.
		strays := maps.Clone(extra)
		strays[".stipulator/bindings/stray.md"] = "not a record\n"
		if _, err := records.Load(proptest.FS(files, strays)); err == nil {
			rt.Fatal("stray file in a record directory loaded silently")
		}
	})
}
