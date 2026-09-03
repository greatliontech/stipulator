package verify

import (
	"reflect"
	"testing"
	"testing/fstest"

	"github.com/greatliontech/stipulator/internal/compile"
	"github.com/greatliontech/stipulator/internal/records"
	"github.com/greatliontech/stipulator/stipulate"
)

// TestHygieneIsRunsRecordOnlyJudgment pins that Hygiene and Run share
// one judgment: over records carrying every record-only fault class of
// bindings, attestations, and gaps, the pre-run report equals the full
// pass's problems — same set, same order — so the two cannot drift
// apart; the classes' own messages are pinned by the verify tests.
func TestHygieneIsRunsRecordOnlyJudgment(t *testing.T) {
	stipulate.Covers(t, "REQ-check-preparation")
	fsys := fstest.MapFS{
		".stipulator/manifest.textproto": {Data: []byte("include: \"specs/**/*.md\"\n")},
		"specs/a.md":                     {Data: []byte(goodDoc)},
		".stipulator/bindings/b.textproto": {Data: []byte(
			"bindings {\n  requirement_id: \"REQ-v-a\"\n  backend: \"go\"\n  symbol: \"x.A\"\n  role: BINDING_ROLE_IMPLEMENTS\n}\n" +
				"bindings {\n  requirement_id: \"REQ-v-a\"\n  backend: \"go\"\n  symbol: \"x.A\"\n  role: BINDING_ROLE_IMPLEMENTS\n}\n" +
				"bindings {\n  requirement_id: \"REQ-v-b\"\n  backend: \"go\"\n  role: BINDING_ROLE_IMPLEMENTS\n}\n" +
				"bindings {\n  requirement_id: \"REQ-v-zz\"\n  backend: \"go\"\n  symbol: \"x.Z\"\n  role: BINDING_ROLE_IMPLEMENTS\n}\n" +
				"bindings {\n  symbol: \"x.N\"\n}\n")},
		".stipulator/attestations/a.textproto": {Data: []byte(
			"attestations {\n  reason: \"nameless\"\n}\n" +
				"attestations {\n  requirement_id: \"REQ-v-a\"\n}\n" +
				"attestations {\n  requirement_id: \"REQ-v-b\"\n  reason: \"judged\"\n}\n" +
				"attestations {\n  requirement_id: \"REQ-v-b\"\n  reason: \"judged twice\"\n}\n" +
				"attestations {\n  requirement_id: \"REQ-v-zz\"\n  reason: \"judged\"\n}\n")},
		".stipulator/gaps/g1.textproto": {Data: []byte("requirement_id: \"REQ-v-b\"\nreason: \"deferred\"\nlands { manual { condition: \"later\" } }\n")},
		".stipulator/gaps/g2.textproto": {Data: []byte("requirement_id: \"REQ-v-b\"\n")},
		".stipulator/gaps/g3.textproto": {Data: []byte("requirement_id: \"REQ-v-zz\"\nreason: \"deferred\"\nlands { manual { condition: \"later\" } }\n")},
		".stipulator/gaps/g4.textproto": {Data: []byte("reason: \"nameless\"\nlands { manual { condition: \"later\" } }\n")},
	}
	spec, diags, err := compile.Compile(fsys)
	if err != nil || len(diags) > 0 {
		t.Fatalf("compile: %v %v", err, diags)
	}
	store, err := records.Load(fsys)
	if err != nil {
		t.Fatal(err)
	}
	got := Hygiene(spec, store)
	want := Run(spec, store, nil, nil).Problems
	// Every class named in the doc: bindings ×6 (duplicate, no symbol,
	// unknown requirement, no requirement/backend/role), attestations ×4
	// (no requirement, no reason, duplicate, unknown requirement) plus
	// the gap contradiction on REQ-v-b, gaps ×5 (duplicate, no reason,
	// no landing condition, unknown requirement, no requirement).
	if len(got) < 16 {
		t.Fatalf("hygiene reported %d problems, want every record-only class: %v", len(got), got)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("hygiene (%d) = %v\nrun (%d) = %v", len(got), got, len(want), want)
	}
}
