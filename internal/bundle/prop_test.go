package bundle

import (
	"testing"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/compile"
	"github.com/greatliontech/stipulator/internal/proptest"
	"github.com/greatliontech/stipulator/stipulate"
	"google.golang.org/protobuf/proto"
	"pgregory.net/rapid"
)

// TestPropBundleDeterminism quantifies REQ-core-determinism over bundle
// export: byte-identical corpora and identifier sets yield identical
// closures and byte-identical rendered documents on every run.
func TestPropBundleDeterminism(t *testing.T) {
	stipulate.Covers(t, "REQ-core-determinism")
	rapid.Check(t, func(rt *rapid.T) {
		c := proptest.Gen(rt)
		files := c.Partition(rt, "p")
		ids := rapid.SliceOfNDistinct(rapid.SampledFrom(c.ReqIDs), 1, 3, rapid.ID[string]).Draw(rt, "ids")
		run := func() (*stipulatorv1.Spec, string) {
			spec, diags, err := compile.Compile(proptest.FS(files, nil))
			if err != nil || len(diags) > 0 {
				rt.Fatalf("compile: %v %v", err, diags)
			}
			b, err := Compute(spec, ids)
			if err != nil {
				rt.Fatalf("compute: %v", err)
			}
			return b, Markdown(b, ids)
		}
		specA, docA := run()
		specB, docB := run()
		if !proto.Equal(specA, specB) {
			rt.Fatal("bundle closure differs across identical runs")
		}
		if docA != docB {
			rt.Fatalf("bundle document differs across identical runs:\n%s\n---\n%s", docA, docB)
		}
	})
}
