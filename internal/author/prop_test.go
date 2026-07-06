package author

import (
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/proptest"
	"github.com/greatliontech/stipulator/internal/records"
	"github.com/greatliontech/stipulator/stipulate"
	"google.golang.org/protobuf/encoding/prototext"
	"pgregory.net/rapid"
)

// recordPath reports whether p is a sanctioned record home.
func recordPath(p string) bool {
	return strings.HasPrefix(p, records.BindingsDir+"/") ||
		strings.HasPrefix(p, records.GapsDir+"/") ||
		p == records.TombstonesPath
}

// parseRecord strict-parses an update's content under the schema its
// path implies; nil content is a deletion and parses vacuously.
func parseRecord(rt *rapid.T, u Update) {
	if u.Content == nil {
		return
	}
	var err error
	switch {
	case strings.HasPrefix(u.Path, records.BindingsDir+"/"):
		err = prototext.Unmarshal(u.Content, &stipulatorv1.BindingSet{})
	case strings.HasPrefix(u.Path, records.GapsDir+"/"):
		err = prototext.Unmarshal(u.Content, &stipulatorv1.Gap{})
	case u.Path == records.TombstonesPath:
		err = prototext.Unmarshal(u.Content, &stipulatorv1.Tombstones{})
	}
	if err != nil {
		rt.Fatalf("%s does not strict-parse under its record schema: %v\n%s", u.Path, err, u.Content)
	}
}

// TestPropVerbsWriteOnlyRecords quantifies disposition transience and
// record scope: every record verb's persistent effect is confined to the
// record homes, every write strict-parses under its schema — the schemas
// admit no status, ordering, or narrative fields — and the outputs are
// pure functions of their inputs (identical on a second run over the
// same state).
func TestPropVerbsWriteOnlyRecords(t *testing.T) {
	stipulate.Covers(t, "REQ-change-transient", "REQ-core-scope")
	rapid.Check(t, func(rt *rapid.T) {
		c := proptest.Gen(rt)
		files := c.Partition(rt, "p")
		target := rapid.SampledFrom(c.ReqIDs).Draw(rt, "target")

		// All randomness is drawn here; the verb closure is a pure
		// function of (filesystem, drawn values), so running it twice
		// over the same state must be identical.
		var run func(fsys fstest.MapFS) ([]Update, error)
		switch rapid.SampledFrom([]string{"bind", "gap", "editorial", "retire", "unbind"}).Draw(rt, "verb") {
		case "bind":
			role := stipulatorv1.BindingRole_BINDING_ROLE_IMPLEMENTS
			if rapid.Bool().Draw(rt, "roleTests") {
				role = stipulatorv1.BindingRole_BINDING_ROLE_TESTS
			}
			run = func(fsys fstest.MapFS) ([]Update, error) {
				up, err := Bind(fsys, nil, BindRequest{
					Requirement: target, Symbol: "example.com/p.F", Backend: "go", Role: role,
				})
				if err != nil {
					return nil, err
				}
				return []Update{*up}, nil
			}
		case "gap":
			run = func(fsys fstest.MapFS) ([]Update, error) {
				g := &stipulatorv1.Gap{}
				g.SetRequirementId(target)
				g.SetReason("generated reason")
				lands, err := NewLandingCondition("", "", "generated condition")
				if err != nil {
					return nil, err
				}
				g.SetLands(lands)
				up, err := Gap(fsys, g)
				if err != nil {
					return nil, err
				}
				return []Update{*up}, nil
			}
		case "editorial":
			// A stale binding to re-pin is part of the generated state.
			run = func(fsys fstest.MapFS) ([]Update, error) {
				fsys[".stipulator/bindings/gen.textproto"] = &fstest.MapFile{
					Data: []byte(proptest.BindingText(target, strings.Repeat("0", 64))),
				}
				return Editorial(fsys, target)
			}
		case "retire":
			// The victim is absent from the corpus but named by a
			// record — the retire precondition.
			run = func(fsys fstest.MapFS) ([]Update, error) {
				fsys[".stipulator/bindings/gen.textproto"] = &fstest.MapFile{
					Data: []byte(proptest.BindingText("REQ-p-ghost", "")),
				}
				return Retire(fsys, "REQ-p-ghost", false)
			}
		case "unbind":
			run = func(fsys fstest.MapFS) ([]Update, error) {
				fsys[".stipulator/bindings/gen.textproto"] = &fstest.MapFile{
					Data: []byte(proptest.BindingText(target, "")),
				}
				ups, _, err := Unbind(fsys, target, "example.com/p.F", stipulatorv1.BindingRole_BINDING_ROLE_IMPLEMENTS)
				return ups, err
			}
		}

		first, err := run(proptest.FS(files, nil))
		if err != nil {
			rt.Fatalf("verb failed: %v", err)
		}
		if len(first) == 0 {
			rt.Fatal("verb returned no updates")
		}
		for _, u := range first {
			if !recordPath(u.Path) {
				rt.Fatalf("verb wrote outside the record homes: %s", u.Path)
			}
			parseRecord(rt, u)
		}

		second, err := run(proptest.FS(files, nil))
		if err != nil {
			rt.Fatalf("second run failed: %v", err)
		}
		if !reflect.DeepEqual(first, second) {
			rt.Fatalf("verb output differs across identical runs:\n%+v\n---\n%+v", first, second)
		}
	})
}
