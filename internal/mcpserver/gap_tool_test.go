package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/greatliontech/gofresh"
	"github.com/modelcontextprotocol/go-sdk/mcp"

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
		s.runTests = func(_ context.Context, scope map[gofresh.Subject]bool) (*verify.TestRun, error) {
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
		s.runTests = func(_ context.Context, scope map[gofresh.Subject]bool) (*verify.TestRun, error) {
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
		s.runTests = func(context.Context, map[gofresh.Subject]bool) (*verify.TestRun, error) {
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
		s.runTests = func(context.Context, map[gofresh.Subject]bool) (*verify.TestRun, error) {
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
}
