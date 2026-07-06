package diff

import (
	"reflect"
	"testing"

	"github.com/greatliontech/stipulator/internal/compile"
	"github.com/greatliontech/stipulator/internal/proptest"
	"github.com/greatliontech/stipulator/stipulate"
	"pgregory.net/rapid"
)

// TestPropDiffDeterminism quantifies REQ-core-determinism over IR diff:
// byte-identical corpus pairs yield identical reports on every run,
// including the self-diff (which must also be empty).
func TestPropDiffDeterminism(t *testing.T) {
	stipulate.Covers(t, "REQ-core-determinism")
	rapid.Check(t, func(rt *rapid.T) {
		gen := func(label string) map[string]string {
			c := proptest.Gen(rt)
			return c.Partition(rt, label)
		}
		oldFiles, newFiles := gen("old"), gen("new")
		run := func() *Report {
			oldSpec, diags, err := compile.Compile(proptest.FS(oldFiles, nil))
			if err != nil || len(diags) > 0 {
				rt.Fatalf("compile old: %v %v", err, diags)
			}
			newSpec, diags, err := compile.Compile(proptest.FS(newFiles, nil))
			if err != nil || len(diags) > 0 {
				rt.Fatalf("compile new: %v %v", err, diags)
			}
			return Diff(oldSpec, newSpec)
		}
		a, b := run(), run()
		if !reflect.DeepEqual(a, b) {
			rt.Fatalf("diff output differs across identical runs:\n%+v\n---\n%+v", a, b)
		}

		selfSpec, diags, err := compile.Compile(proptest.FS(oldFiles, nil))
		if err != nil || len(diags) > 0 {
			rt.Fatalf("compile: %v %v", err, diags)
		}
		self := Diff(selfSpec, selfSpec)
		if !reflect.DeepEqual(self, &Report{}) {
			rt.Fatalf("self-diff not empty: %+v", self)
		}
	})
}
