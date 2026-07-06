package verify

import (
	"fmt"
	"strings"
	"testing"

	"github.com/greatliontech/stipulator/internal/compile"
	"github.com/greatliontech/stipulator/internal/proptest"
	"github.com/greatliontech/stipulator/internal/records"
	"github.com/greatliontech/stipulator/stipulate"
	"pgregory.net/rapid"
)

// TestPropDanglingRecordsAreProblems quantifies the dangling invariant:
// a binding or gap record naming an identity outside the corpus is a
// verification problem, and records naming declared identities never
// produce one.
func TestPropDanglingRecordsAreProblems(t *testing.T) {
	stipulate.Covers(t, "REQ-change-dangling")
	rapid.Check(t, func(rt *rapid.T) {
		c := proptest.Gen(rt)
		files := c.Partition(rt, "p")

		// Records naming declared requirements, with one optionally
		// corrupted to a ghost identity.
		ghost := fmt.Sprintf("REQ-p-ghost%d", rapid.IntRange(0, 9).Draw(rt, "ghost"))
		corrupt := rapid.Bool().Draw(rt, "corrupt")
		asGap := rapid.Bool().Draw(rt, "asGap")

		bound := rapid.SampledFrom(c.ReqIDs).Draw(rt, "bound")
		bindingID, gapID := bound, bound
		if corrupt {
			if asGap {
				gapID = ghost
			} else {
				bindingID = ghost
			}
		}
		extra := map[string]string{
			".stipulator/bindings/p.textproto": proptest.BindingText(bindingID, ""),
			".stipulator/gaps/p.textproto":     proptest.GapText(gapID),
		}

		spec, diags, err := compile.Compile(proptest.FS(files, extra))
		if err != nil || len(diags) > 0 {
			rt.Fatalf("compile: %v %v", err, diags)
		}
		store, err := records.Load(proptest.FS(files, extra))
		if err != nil {
			rt.Fatal(err)
		}
		rep := Run(spec, store, nil, nil)

		var dangling []string
		for _, p := range rep.Problems {
			if strings.Contains(p.Message, "not in the corpus") {
				dangling = append(dangling, p.Message)
			}
		}
		if corrupt && len(dangling) == 0 {
			rt.Fatalf("record naming %s produced no dangling problem: %+v", ghost, rep.Problems)
		}
		if corrupt && !strings.Contains(strings.Join(dangling, " "), ghost) {
			rt.Fatalf("dangling problem does not name %s: %v", ghost, dangling)
		}
		if !corrupt && len(dangling) != 0 {
			rt.Fatalf("clean records produced dangling problems: %v", dangling)
		}
	})
}
