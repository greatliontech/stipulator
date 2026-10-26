package facts

import (
	"reflect"
	"testing"

	"github.com/greatliontech/stipulator/internal/compile"
	"github.com/greatliontech/stipulator/internal/proptest"
	"github.com/greatliontech/stipulator/internal/records"
	"github.com/greatliontech/stipulator/stipulate"
	"pgregory.net/rapid"
)

// TestPropSeedsDeterminism quantifies REQ-core-determinism over facts
// seed derivation: byte-identical corpora, record stores, and identifier
// sets yield identical seeds on every run.
func TestPropSeedsDeterminism(t *testing.T) {
	stipulate.Covers(t, "REQ-core-determinism")
	rapid.Check(t, func(rt *rapid.T) {
		c := proptest.Gen(rt)
		files := c.Partition(rt, "p")
		extra := map[string]string{
			".stipulator/bindings/p.textproto": proptest.BindingText(rapid.SampledFrom(c.ReqIDs).Draw(rt, "bound"), ""),
		}
		ids := rapid.SliceOfNDistinct(rapid.SampledFrom(c.ReqIDs), 1, 3, rapid.ID[string]).Draw(rt, "ids")
		run := func() []Seed {
			fsys := proptest.FS(files, extra)
			spec, diags, err := compile.Compile(fsys)
			if err != nil || len(diags) > 0 {
				rt.Fatalf("compile: %v %v", err, diags)
			}
			store, err := records.Load(fsys)
			if err != nil {
				rt.Fatal(err)
			}
			seeds, err := Seeds(spec, store, ids)
			if err != nil {
				rt.Fatalf("seeds: %v", err)
			}
			return seeds
		}
		a, b := run(), run()
		if !reflect.DeepEqual(a, b) {
			rt.Fatalf("seeds differ across identical runs:\n%+v\n---\n%+v", a, b)
		}
	})
}
