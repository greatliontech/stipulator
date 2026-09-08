package mcpserver

import (
	"context"
	"encoding/json"
	"github.com/greatliontech/stipulator/internal/backends/golang"
	"strings"
	"testing"

	"github.com/greatliontech/gofresh"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/protobuf/encoding/protojson"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/stipulate"
)

// The gap tool mirrors the operation's batch semantics: comma-separated
// requirements share one declaration, the self sentinel lands each on
// its own coverage, fired alone fires existing manual conditions, and
// retract deletes records — dangling ones included.
//
//gofresh:pure
func TestGapToolBatchFireRetract(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-tools", "REQ-gap-bulk", "REQ-gap-retract")
	danglingPath := ".stipulator/gaps/ghost.textproto"
	sess, writes := harness(t, map[string]string{
		danglingPath: "requirement_id: \"REQ-m-ghost\"\nreason: \"r\"\nlands { manual { condition: \"c\" } }\n",
	})

	// Batch declare with the self sentinel: one call, two records, each
	// landing on its own coverage.
	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "gap", Arguments: map[string]any{
		"requirement": "REQ-m-a,REQ-m-b", "reason": "spec ahead of code", "covered": "self",
	}})
	if err != nil || res.IsError {
		t.Fatalf("gap batch: %v %+v", err, res)
	}
	aPath, bPath := ".stipulator/gaps/m-a.textproto", ".stipulator/gaps/m-b.textproto"
	for p, self := range map[string]string{aPath: "REQ-m-a", bPath: "REQ-m-b"} {
		c, ok := writes[p]
		if !ok || !strings.Contains(string(c), "covered: \""+self+"\"") {
			t.Fatalf("%s missing or not self-landed:\n%s", p, c)
		}
	}

	// Declare a manual gap, then fire it through the tool.
	if res, err = sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "gap", Arguments: map[string]any{
		"requirement": "REQ-m-a", "reason": "deferred", "manual": "externally judged",
	}}); err != nil || res.IsError {
		t.Fatalf("gap manual: %v %+v", err, res)
	}
	// The contradicted class rides a manual condition only, and is a
	// declaration field: refused on a retract, a bare fire, and the list.
	if res, err = sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "gap", Arguments: map[string]any{
		"requirement": "REQ-m-b", "reason": "the schema contradicts the letter", "covered": "self", "contradicted": true,
	}}); err != nil || !res.IsError {
		t.Fatalf("contradicted with a machine condition did not refuse: %v %+v", err, res)
	}
	for _, args := range []map[string]any{
		{"requirement": "REQ-m-a", "retract": true, "contradicted": true},
		{"requirement": "REQ-m-a", "fired": true, "contradicted": true},
		{"list": true, "contradicted": true},
	} {
		if res, err = sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "gap", Arguments: args}); err != nil || !res.IsError {
			t.Fatalf("%v did not refuse the stray class: %v %+v", args, err, res)
		}
	}
	if res, err = sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "gap", Arguments: map[string]any{
		"requirement": "REQ-m-b", "reason": "the schema contradicts the letter", "manual": "the derivation lands", "contradicted": true,
	}}); err != nil || res.IsError {
		t.Fatalf("gap contradicted: %v %+v", err, res)
	}
	if c := writes[bPath]; !strings.Contains(string(c), "contradicted: true") {
		t.Fatalf("declaration lost the class:\n%s", c)
	}
	if res, err = sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "gap", Arguments: map[string]any{
		"requirement": "REQ-m-a", "fired": true,
	}}); err != nil || res.IsError {
		t.Fatalf("gap fire: %v %+v", err, res)
	}
	if c := writes[aPath]; !strings.Contains(string(c), "fired: true") {
		t.Fatalf("fire left the record unfired:\n%s", c)
	}

	// Retract both live records and the dangling one in one batch.
	res, err = sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "gap", Arguments: map[string]any{
		"requirement": "REQ-m-a,REQ-m-b,REQ-m-ghost", "retract": true,
	}})
	if err != nil || res.IsError {
		t.Fatalf("gap retract: %v %+v", err, res)
	}
	for _, p := range []string{aPath, bPath, danglingPath} {
		if c, ok := writes[p]; !ok || c != nil {
			t.Fatalf("%s not deleted (ok=%v)", p, ok)
		}
	}
	b, _ := json.Marshal(res.StructuredContent)
	if !strings.Contains(string(b), danglingPath) {
		t.Fatalf("retraction result does not name the dangling record: %s", b)
	}

	// A retract naming a requirement with no record errors — and, being
	// all-or-nothing, deletes nothing else with it.
	res, err = sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "gap", Arguments: map[string]any{
		"requirement": "REQ-m-b", "retract": true,
	}})
	if err != nil || !res.IsError {
		t.Fatalf("retract of a record-less requirement did not error: %v %+v", err, res)
	}
}

// The prune tool's dangling mode deletes only corpus-orphaned gap
// records — judged from corpus and records alone — and its check form
// deletes nothing; the ordinary resolved-mode prune never touches a
// dangling record.
//
//gofresh:pure
func TestPruneToolDanglingMode(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-tools", "REQ-gap-prune-dangling")
	danglingPath := ".stipulator/gaps/ghost.textproto"
	livePath := ".stipulator/gaps/m-b.textproto"
	sess, writes := harness(t, map[string]string{
		danglingPath: "requirement_id: \"REQ-m-ghost\"\nreason: \"r\"\nlands { manual { condition: \"c\" } }\n",
		livePath:     "requirement_id: \"REQ-m-b\"\nreason: \"later\"\nlands { manual { condition: \"c\" } }\n",
	})

	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "prune", Arguments: map[string]any{
		"dangling": true, "check": true,
	}})
	if err != nil || res.IsError {
		t.Fatalf("prune dangling check: %v %+v", err, res)
	}
	b, _ := json.Marshal(res.StructuredContent)
	if !strings.Contains(string(b), danglingPath) {
		t.Fatalf("check did not report the dangling record: %s", b)
	}
	if _, touched := writes[danglingPath]; touched {
		t.Fatal("check deleted a record — must be dry-run")
	}

	// The ordinary resolved-mode prune never deletes a dangling record:
	// the dangling gap is a verification problem, and problems refuse the
	// resolved-mode prune outright — the dangling id is filtered from the
	// witness scope rather than refused as an unknown identifier, so the
	// refusal names the real cause.
	res, err = sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "prune", Arguments: map[string]any{}})
	if err != nil || !res.IsError {
		t.Fatalf("resolved-mode prune over a dangling record did not refuse: %v %+v", err, res)
	}
	if txt := toolText(t, res); !strings.Contains(txt, "verification problems") {
		t.Fatalf("refusal does not name the verification problems: %q", txt)
	}
	if _, touched := writes[danglingPath]; touched {
		t.Fatal("resolved-mode prune touched the dangling record")
	}

	res, err = sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "prune", Arguments: map[string]any{
		"dangling": true,
	}})
	if err != nil || res.IsError {
		t.Fatalf("prune dangling: %v %+v", err, res)
	}
	if c, ok := writes[danglingPath]; !ok || c != nil {
		t.Fatalf("dangling record not deleted (ok=%v)", ok)
	}
	if _, touched := writes[livePath]; touched {
		t.Fatal("dangling mode deleted a live record")
	}
}

// The gap list is the read surface: every record's declaration fields
// beside its evaluated state — resolved, due, open, dangling — with the
// witness evaluation scoped exactly as prune's, dangling records listed
// rather than refused, verification problems a stated caveat, and the
// write forms refused in combination (REQ-gap-list).
func TestGapToolListRowsStatesAndScope(t *testing.T) {
	stipulate.Covers(t, "REQ-gap-list", "REQ-gap-lifecycle", "REQ-mcp-tools")
	var got map[gofresh.Subject]bool
	// The class rides every row shape: a resolved row keeps it and
	// leaves the count, an open row carries it and counts, a machine row
	// cannot carry it, and a dangling row names it while counting in
	// dangling alone.
	sess, writes := harnessWith(t, map[string]string{
		// Every count on the line pairwise distinct — 5 open, 3 due, 1
		// resolved, 2 dangling, 4 contradicted — so a count sourced from
		// the wrong field, or two counts swapped, changes the string.
		"specs/a.md":                       doc + "\n**REQ-m-c** (behavior): It MUST z.\n\n**REQ-m-d** (behavior): It MUST w.\n\n**REQ-m-e** (behavior): It MUST v.\n\n**REQ-m-f** (behavior): It MUST u.\n\n**REQ-m-g** (behavior): It MUST t.\n\n**REQ-m-h** (behavior): It MUST s.\n\n**REQ-m-i** (behavior): It MUST r.\n",
		".stipulator/bindings/a.textproto": pinnedBindingFor(t, "REQ-m-a", "example.com/p.TestA", "s"),
		".stipulator/gaps/m-a.textproto": "requirement_id: \"REQ-m-a\"\nreason: \"external judgment\"\n" +
			"lands {\n  manual {\n    condition: \"judged done\"\n    fired: true\n    contradicted: true\n  }\n}\n",
		".stipulator/gaps/m-b.textproto": "requirement_id: \"REQ-m-b\"\nreason: \"lands with the sibling\"\n" +
			"lands {\n  covered: \"REQ-m-a\"\n}\n",
		".stipulator/gaps/m-c.textproto": "requirement_id: \"REQ-m-c\"\nreason: \"the letter is contradicted by design\"\n" +
			"lands {\n  manual {\n    condition: \"the derivation lands\"\n    contradicted: true\n  }\n}\n",
		".stipulator/gaps/m-d.textproto": "requirement_id: \"REQ-m-d\"\nreason: \"the other letter is contradicted by design\"\n" +
			"lands {\n  manual {\n    condition: \"the second derivation lands\"\n    contradicted: true\n  }\n}\n",
		".stipulator/gaps/m-e.textproto": "requirement_id: \"REQ-m-e\"\nreason: \"deferred\"\n" +
			"lands {\n  manual {\n    condition: \"ops signs off\"\n  }\n}\n",
		".stipulator/gaps/m-f.textproto": "requirement_id: \"REQ-m-f\"\nreason: \"deferred\"\n" +
			"lands {\n  manual {\n    condition: \"ops signs off\"\n  }\n}\n",
		".stipulator/gaps/m-g.textproto": "requirement_id: \"REQ-m-g\"\nreason: \"deferred\"\n" +
			"lands {\n  manual {\n    condition: \"ops signs off\"\n  }\n}\n",
		".stipulator/gaps/m-h.textproto": "requirement_id: \"REQ-m-h\"\nreason: \"contradicted until the fire, fired\"\n" +
			"lands {\n  manual {\n    condition: \"the third derivation lands\"\n    fired: true\n    contradicted: true\n  }\n}\n",
		".stipulator/gaps/m-i.textproto": "requirement_id: \"REQ-m-i\"\nreason: \"contradicted until the fire, fired\"\n" +
			"lands {\n  manual {\n    condition: \"the fourth derivation lands\"\n    fired: true\n    contradicted: true\n  }\n}\n",
		".stipulator/gaps/ghost2.textproto": "requirement_id: \"REQ-m-ghost2\"\nreason: \"left behind too\"\n" +
			"lands {\n  manual {\n    condition: \"c\"\n  }\n}\n",
		".stipulator/gaps/ghost.textproto": "requirement_id: \"REQ-m-ghost\"\nreason: \"left behind\"\n" +
			"lands {\n  manual {\n    condition: \"c\"\n    contradicted: true\n  }\n}\n",
	}, func(s *Server) {
		s.runTests = func(_ context.Context, _ *golang.Capture, _ verify.WitnessSeeding, scope map[gofresh.Subject]bool) (*verify.TestRun, error) {
			got = scope
			return &verify.TestRun{
				RaceEnabled:      true,
				SelectiveServing: true,
				Outcomes:         map[string]verify.TestOutcome{"example.com/p.TestA": verify.TestPassed},
			}, nil
		}
	})
	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "gap", Arguments: map[string]any{"list": true}})
	if err != nil || res.IsError {
		t.Fatalf("gap list: %v %+v", err, res)
	}
	if len(got) != 1 || !got[gofresh.Subject{Package: "example.com/p", Symbol: "TestA"}] {
		t.Fatalf("witness scope = %v, want exactly the gap-relevant bound subject", got)
	}
	b, _ := json.Marshal(res.StructuredContent)
	var out struct {
		Gaps  []json.RawMessage `json:"gaps"`
		Notes []string          `json:"notes"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Gaps) != 11 {
		t.Fatalf("rows = %d, want 11:\n%s", len(out.Gaps), b)
	}
	states := map[string]*stipulatorv1.GapReport{}
	for _, raw := range out.Gaps {
		g := &stipulatorv1.GapReport{}
		if err := protojson.Unmarshal(raw, g); err != nil {
			t.Fatalf("row is not a strict GapReport: %v\n%s", err, raw)
		}
		states[g.GetRequirementId()] = g
	}
	if g := states["REQ-m-a"]; g.GetState() != stipulatorv1.GapState_GAP_STATE_RESOLVED ||
		!g.GetFired() || !g.GetContradicted() || g.GetCondition() != "manual: judged done" || g.GetReason() != "external judgment" {
		t.Fatalf("resolved row wrong: %v", g)
	}
	for _, id := range []string{"REQ-m-c", "REQ-m-d"} {
		if g := states[id]; g.GetState() != stipulatorv1.GapState_GAP_STATE_OPEN || !g.GetContradicted() || g.GetFired() {
			t.Fatalf("open contradicted row %s wrong: %v", id, g)
		}
	}
	// A fired contradicted gap on an uncovered requirement is due — the
	// condition holds, the witness is still wanted — and counts among
	// the contradicted like any unresolved row.
	for _, id := range []string{"REQ-m-h", "REQ-m-i"} {
		if g := states[id]; g.GetState() != stipulatorv1.GapState_GAP_STATE_DUE || !g.GetContradicted() || !g.GetFired() {
			t.Fatalf("due contradicted row %s wrong: %v", id, g)
		}
	}
	if g := states["REQ-m-b"]; g.GetState() != stipulatorv1.GapState_GAP_STATE_DUE ||
		g.GetCondition() != "covered(REQ-m-a)" || g.GetFired() {
		t.Fatalf("due row wrong: %v", g)
	}
	if g := states["REQ-m-ghost"]; g.GetState() != stipulatorv1.GapState_GAP_STATE_DANGLING || !g.GetContradicted() {
		t.Fatalf("dangling row wrong (state and class): %v", g)
	}
	if g := states["REQ-m-ghost2"]; g.GetState() != stipulatorv1.GapState_GAP_STATE_DANGLING || g.GetContradicted() {
		t.Fatalf("plain dangling row wrong: %v", g)
	}
	if g := states["REQ-m-b"]; g.GetContradicted() {
		t.Fatalf("a machine row carries the class: %v", g)
	}
	// The dangling record is a verification problem — a caveat on the
	// listing, never a refusal.
	if len(out.Notes) == 0 || !strings.Contains(out.Notes[0], "verification problems") {
		t.Fatalf("problems caveat missing: %s", b)
	}
	// Four contradicted on the line (two open, two due): the resolved
	// row left the count and the dangling row counts in dangling alone;
	// every count distinct, so no swap of two reproduces the string.
	if text := toolText(t, res); !strings.Contains(text, "11 gap records: 5 open, 3 due, 1 resolved, 2 dangling, 4 contradicted") {
		t.Fatalf("list line does not carry the counts, the class apart: %q", text)
	}
	if len(writes) != 0 {
		t.Fatalf("the read surface wrote: %v", writes)
	}

	res, err = sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "gap", Arguments: map[string]any{"list": true, "requirement": "REQ-m-a"}})
	if err != nil || !res.IsError {
		t.Fatalf("list combined with a write field did not refuse: %v %+v", err, res)
	}
}

// A gapless tree lists empty without gathering witness evidence — while
// a broken corpus still refuses: the empty answer skips evidence, never
// diagnostics.
func TestGapToolListNoGapsSkipsWitnessEvaluation(t *testing.T) {
	stipulate.Covers(t, "REQ-gap-list")
	called := false
	sess, _ := harnessWith(t, map[string]string{}, func(s *Server) {
		s.runTests = func(context.Context, *golang.Capture, verify.WitnessSeeding, map[gofresh.Subject]bool) (*verify.TestRun, error) {
			called = true
			return &verify.TestRun{SelectiveServing: true}, nil
		}
	})
	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "gap", Arguments: map[string]any{"list": true}})
	if err != nil || res.IsError {
		t.Fatalf("gap list: %v %+v", err, res)
	}
	if called {
		t.Fatal("witness evaluation ran with no gap records")
	}
	if text := toolText(t, res); !strings.Contains(text, "no gap records") {
		t.Fatalf("empty list does not name itself: %q", text)
	}

	broken, _ := harnessWith(t, map[string]string{
		".stipulator/manifest.textproto": ":::garbage\n",
	}, func(s *Server) {
		s.runTests = func(context.Context, *golang.Capture, verify.WitnessSeeding, map[gofresh.Subject]bool) (*verify.TestRun, error) {
			t.Error("witness evaluation ran on a broken corpus")
			return &verify.TestRun{SelectiveServing: true}, nil
		}
	})
	res, err = broken.CallTool(context.Background(), &mcp.CallToolParams{Name: "gap", Arguments: map[string]any{"list": true}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatalf("broken corpus listed as success: %+v", res)
	}
}

// With gap records present but no gap-relevant bound witness, the
// evaluation is witness-free — the skip that lets spec-only design
// trees list (REQ-gap-list).
func TestGapToolListEmptyScopeSkipsWitnessRun(t *testing.T) {
	stipulate.Covers(t, "REQ-gap-list")
	called := false
	sess, _ := harnessWith(t, map[string]string{
		".stipulator/gaps/m-b.textproto": "requirement_id: \"REQ-m-b\"\nreason: \"spec ahead of code\"\n" +
			"lands {\n  manual {\n    condition: \"c\"\n  }\n}\n",
	}, func(s *Server) {
		s.runTests = func(context.Context, *golang.Capture, verify.WitnessSeeding, map[gofresh.Subject]bool) (*verify.TestRun, error) {
			called = true
			return &verify.TestRun{SelectiveServing: true}, nil
		}
	})
	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "gap", Arguments: map[string]any{"list": true}})
	if err != nil || res.IsError {
		t.Fatalf("gap list: %v %+v", err, res)
	}
	if called {
		t.Fatal("witness evaluation ran with no gap-relevant bound witness")
	}
	b, _ := json.Marshal(res.StructuredContent)
	if !strings.Contains(string(b), "GAP_STATE_OPEN") || !strings.Contains(string(b), "REQ-m-b") {
		t.Fatalf("unbound gapped requirement's row missing or misclassified: %s", b)
	}
}

// A resolved-mode prune gathers witness evidence only for the gapped
// requirements: the scope handed to the witness run is exactly the
// gap-bound subjects — a witness bound only to ungapped requirements
// never executes for pruning — and the result names the evaluation
// performed (REQ-gap-resolved-pruned).
func TestPruneToolScopesWitnessEvaluationToGappedRequirements(t *testing.T) {
	stipulate.Covers(t, "REQ-gap-resolved-pruned", "REQ-mcp-tools")
	gapPath := ".stipulator/gaps/m-a.textproto"
	var got map[gofresh.Subject]bool
	sess, writes := harnessWith(t, map[string]string{
		".stipulator/bindings/a.textproto": pinnedBindingFor(t, "REQ-m-a", "example.com/p.TestA", "s"),
		".stipulator/bindings/b.textproto": pinnedBindingFor(t, "REQ-m-b", "example.com/q.TestA", "q"),
		gapPath: "requirement_id: \"REQ-m-a\"\nreason: \"pending\"\n" +
			"lands {\n  manual {\n    condition: \"judged done\"\n    fired: true\n  }\n}\n",
	}, func(s *Server) {
		s.runTests = func(_ context.Context, _ *golang.Capture, _ verify.WitnessSeeding, scope map[gofresh.Subject]bool) (*verify.TestRun, error) {
			got = scope
			return &verify.TestRun{
				RaceEnabled:      true,
				SelectiveServing: true,
				Outcomes:         map[string]verify.TestOutcome{"example.com/p.TestA": verify.TestPassed},
			}, nil
		}
	})
	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "prune", Arguments: map[string]any{}})
	if err != nil || res.IsError {
		t.Fatalf("prune: %v %+v", err, res)
	}
	if len(got) != 1 || !got[gofresh.Subject{Package: "example.com/p", Symbol: "TestA"}] {
		t.Fatalf("witness scope = %v, want exactly the gap-bound subject", got)
	}
	if c, ok := writes[gapPath]; !ok || c != nil {
		t.Fatalf("resolved gap not deleted (ok=%v)", ok)
	}
	if b, _ := json.Marshal(res.StructuredContent); !strings.Contains(string(b), "evaluated 1 gap records") {
		t.Fatalf("result does not name the evaluation performed: %s", b)
	}
}

// A gap's covered(<id>) landing condition reads the target
// requirement's coverage, so the target's bound subjects join the
// witness scope — without them an exempt-arm resolution would misread
// a stale target as unresolved while the gate advertises the gap
// resolved (REQ-gap-resolved-pruned, REQ-gap-lifecycle).
func TestPruneToolScopeIncludesConditionTargets(t *testing.T) {
	stipulate.Covers(t, "REQ-gap-resolved-pruned")
	var got map[gofresh.Subject]bool
	sess, _ := harnessWith(t, map[string]string{
		".stipulator/bindings/a.textproto": pinnedBindingFor(t, "REQ-m-a", "example.com/p.TestA", "s"),
		".stipulator/bindings/b.textproto": pinnedBindingFor(t, "REQ-m-b", "example.com/q.TestA", "q"),
		".stipulator/gaps/m-a.textproto": "requirement_id: \"REQ-m-a\"\nreason: \"pending\"\n" +
			"lands {\n  covered: \"REQ-m-b\"\n}\n",
	}, func(s *Server) {
		s.runTests = func(_ context.Context, _ *golang.Capture, _ verify.WitnessSeeding, scope map[gofresh.Subject]bool) (*verify.TestRun, error) {
			got = scope
			return &verify.TestRun{
				RaceEnabled:      true,
				SelectiveServing: true,
				Outcomes: map[string]verify.TestOutcome{
					"example.com/p.TestA": verify.TestPassed,
					"example.com/q.TestA": verify.TestPassed,
				},
			}, nil
		}
	})
	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "prune", Arguments: map[string]any{"check": true}})
	if err != nil || res.IsError {
		t.Fatalf("prune: %v %+v", err, res)
	}
	if len(got) != 2 || !got[gofresh.Subject{Package: "example.com/q", Symbol: "TestA"}] {
		t.Fatalf("witness scope = %v, want the condition target's subject included", got)
	}
}

// The gapless fast path skips witness evidence, never corpus
// diagnostics: a broken corpus still refuses a gapless prune
// (REQ-gap-resolved-pruned).
func TestPruneToolGaplessTreeStillCompilesTheCorpus(t *testing.T) {
	stipulate.Covers(t, "REQ-gap-resolved-pruned")
	sess, _ := harnessWith(t, map[string]string{
		".stipulator/manifest.textproto": ":::garbage\n",
	}, func(s *Server) {
		s.runTests = func(context.Context, *golang.Capture, verify.WitnessSeeding, map[gofresh.Subject]bool) (*verify.TestRun, error) {
			t.Error("witness evaluation ran on a gapless tree")
			return &verify.TestRun{SelectiveServing: true}, nil
		}
	})
	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "prune", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatalf("broken corpus did not refuse a gapless prune: %+v", res)
	}
}

// With no gap records a resolved-mode prune gathers no witness evidence
// at all — deletion work only (REQ-gap-resolved-pruned).
func TestPruneToolNoGapsSkipsWitnessEvaluation(t *testing.T) {
	stipulate.Covers(t, "REQ-gap-resolved-pruned")
	called := false
	sess, _ := harnessWith(t, map[string]string{
		".stipulator/bindings/a.textproto": pinnedBindingFor(t, "REQ-m-a", "example.com/p.TestA", "s"),
	}, func(s *Server) {
		s.runTests = func(context.Context, *golang.Capture, verify.WitnessSeeding, map[gofresh.Subject]bool) (*verify.TestRun, error) {
			called = true
			return &verify.TestRun{RaceEnabled: true, SelectiveServing: true}, nil
		}
	})
	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "prune", Arguments: map[string]any{}})
	if err != nil || res.IsError {
		t.Fatalf("prune: %v %+v", err, res)
	}
	if called {
		t.Fatal("witness evaluation ran with no gap records")
	}
	if b, _ := json.Marshal(res.StructuredContent); !strings.Contains(string(b), "no gap records - nothing to evaluate") {
		t.Fatalf("fast path does not name itself: %s", b)
	}

	// The gapless arm's preview marks itself too: a zero-row check and
	// a zero-write apply must never be confusable on the wire
	// (REQ-mcp-response-contract).
	res, err = sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "prune", Arguments: map[string]any{"check": true}})
	if err != nil || res.IsError {
		t.Fatalf("gapless prune check: %v %+v", err, res)
	}
	if b, _ := json.Marshal(res.StructuredContent); !strings.Contains(string(b), `"check":true`) {
		t.Fatalf("gapless check preview unmarked: %s", b)
	}
}
