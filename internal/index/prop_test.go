package index

import (
	"reflect"
	"testing"

	"github.com/greatliontech/stipulator/internal/compile"
	"github.com/greatliontech/stipulator/internal/proptest"
	"github.com/greatliontech/stipulator/stipulate"
	"pgregory.net/rapid"
)

// TestPropIndexDeterminism quantifies REQ-core-determinism over index
// generation: byte-identical corpora render byte-identical folder
// indexes on every run.
func TestPropIndexDeterminism(t *testing.T) {
	stipulate.Covers(t, "REQ-core-determinism")
	rapid.Check(t, func(rt *rapid.T) {
		c := proptest.Gen(rt)
		files := c.Partition(rt, "p")
		build := func() map[string][]byte {
			spec, diags, err := compile.Compile(proptest.FS(files, nil))
			if err != nil || len(diags) > 0 {
				rt.Fatalf("compile: %v %v", err, diags)
			}
			return Build(spec)
		}
		a, b := build(), build()
		if !reflect.DeepEqual(a, b) {
			rt.Fatalf("index output differs across identical runs:\n%v\n---\n%v", a, b)
		}
	})
}
