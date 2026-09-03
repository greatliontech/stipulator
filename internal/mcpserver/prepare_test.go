package mcpserver

import (
	"context"
	"strings"
	"testing"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/stipulate"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// textOf concatenates a result's text content.
func textOf(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

// seedingBackend is a fake backend that also classifies witnesses — the
// shape of the real owned child — so the sharing between resolution and
// the witness run can be observed by identity.
type seedingBackend struct{ fakeBackend }

func (seedingBackend) NeverServe([]string) (map[string]string, error) {
	return map[string]string{}, nil
}

// TestWitnessedToolsRefuseBeforeAnyChildOnRecordHygiene pins the
// record-hygiene leg on the MCP surface: records that fail hygiene keep
// the verify and gate tools from opening a resolver or running a
// witness — the verification problems are the answer
// (REQ-check-preparation).
func TestWitnessedToolsRefuseBeforeAnyChildOnRecordHygiene(t *testing.T) {
	stipulate.Covers(t, "REQ-check-preparation")
	opened, ran := 0, 0
	sess, _ := harnessWith(t, map[string]string{
		".stipulator/bindings/ghost.textproto": "bindings {\n  requirement_id: \"REQ-ghost\"\n  backend: \"go\"\n  symbol: \"example.com/p.TestA\"\n  role: BINDING_ROLE_TESTS\n}\n",
	}, func(s *Server) {
		inner := s.backends
		s.backends = func(ctx context.Context) (map[string]verify.Backend, error) { opened++; return inner(ctx) }
		s.runTests = func(context.Context, verify.WitnessSeeding, map[gofresh.Subject]bool) (*verify.TestRun, error) {
			ran++
			return &verify.TestRun{}, nil
		}
	})
	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "verify", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError && !strings.Contains(textOf(res), "1 problems") {
		t.Fatalf("verify answered %s, want the dangling binding reported as a problem", textOf(res))
	}
	if _, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "gate", Arguments: map[string]any{}}); err != nil {
		t.Fatal(err)
	}
	if opened != 0 || ran != 0 {
		t.Fatalf("opened %d resolvers and ran %d witness runs under records that fail hygiene; the refusal fires before any child", opened, ran)
	}
}

// TestGateToolRefusesVocabularyBeforeAnyChild pins the vocabulary leg on
// the MCP gate and verify tools: an unknown view, bucket, filter, or
// identifier refuses before a resolver opens or a witness runs.
func TestGateToolRefusesVocabularyBeforeAnyChild(t *testing.T) {
	stipulate.Covers(t, "REQ-check-preparation")
	opened, ran := 0, 0
	sess, _ := harnessWith(t, nil, func(s *Server) {
		inner := s.backends
		s.backends = func(ctx context.Context) (map[string]verify.Backend, error) { opened++; return inner(ctx) }
		s.runTests = func(context.Context, verify.WitnessSeeding, map[gofresh.Subject]bool) (*verify.TestRun, error) {
			ran++
			return &verify.TestRun{}, nil
		}
	})
	for _, tc := range []struct {
		tool string
		args map[string]any
	}{
		{"gate", map[string]any{"view": "bogus"}}, {"gate", map[string]any{"bucket": "bogus"}}, {"gate", map[string]any{"ids": "REQ-nope"}},
		{"verify", map[string]any{"view": "bogus"}}, {"verify", map[string]any{"filter": "[bad"}}, {"verify", map[string]any{"ids": "REQ-nope"}},
	} {
		res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: tc.tool, Arguments: tc.args})
		if err != nil {
			t.Fatal(err)
		}
		if !res.IsError {
			t.Fatalf("%s %v answered without refusing: %s", tc.tool, tc.args, textOf(res))
		}
	}
	if opened != 0 || ran != 0 {
		t.Fatalf("opened %d resolvers and ran %d witness runs under a refused vocabulary", opened, ran)
	}
}

// TestWitnessRunSharesTheToolsResolverChild pins the shared child: the
// witness run classifies through the very backend the tool opened to
// resolve bindings — one owned process per tool call.
func TestWitnessRunSharesTheToolsResolverChild(t *testing.T) {
	stipulate.Covers(t, "REQ-check-preparation")
	backend := &seedingBackend{fakeBackend{"example.com/p.TestA": strings.Repeat("s", 64)}}
	opened := 0
	var got verify.WitnessSeeding
	sess, _ := harnessWith(t, nil, func(s *Server) {
		s.backends = func(context.Context) (map[string]verify.Backend, error) {
			opened++
			return map[string]verify.Backend{"go": backend}, nil
		}
		s.runTests = func(_ context.Context, seeding verify.WitnessSeeding, _ map[gofresh.Subject]bool) (*verify.TestRun, error) {
			got = seeding
			return &verify.TestRun{RaceEnabled: true, SelectiveServing: true}, nil
		}
	})
	if _, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "verify", Arguments: map[string]any{}}); err != nil {
		t.Fatal(err)
	}
	if opened != 1 || got != verify.WitnessSeeding(backend) {
		t.Fatalf("opened %d backends, witness run seeded by %T: want the one resolver the tool opened", opened, got)
	}
}

// TestWitnessRunRefusesWithoutAResolverChild pins the production witness
// run's guard: handed no classifier it refuses rather than guess — a
// nil seeding would otherwise reach the run and fail closed only by
// panicking.
func TestWitnessRunRefusesWithoutAResolverChild(t *testing.T) {
	stipulate.Covers(t, "REQ-check-preparation")
	_, err := New(t.TempDir()).runTests(context.Background(), nil, nil)
	if err == nil || !strings.Contains(err.Error(), "resolver child") {
		t.Fatalf("err = %v, want the missing-classifier refusal", err)
	}
}

// TestContextToolJudgesNoWitnessOnTheRecordOnlyPass pins the context
// tool's coverage flag: under records that fail hygiene the pass
// witnessed nothing, so a pinned witness-backed requirement must not
// read broken for an outcome no run produced.
func TestContextToolJudgesNoWitnessOnTheRecordOnlyPass(t *testing.T) {
	stipulate.Covers(t, "REQ-check-preparation")
	ran := 0
	sess, _ := harnessWith(t, map[string]string{
		".stipulator/bindings/m.textproto":     pinnedBinding(t),
		".stipulator/bindings/ghost.textproto": "bindings {\n  requirement_id: \"REQ-ghost\"\n  backend: \"go\"\n  symbol: \"example.com/p.TestA\"\n  role: BINDING_ROLE_TESTS\n}\n",
	}, func(s *Server) {
		s.runTests = func(context.Context, verify.WitnessSeeding, map[gofresh.Subject]bool) (*verify.TestRun, error) {
			ran++
			return &verify.TestRun{}, nil
		}
	})
	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "context", Arguments: map[string]any{"ids": "REQ-m-a"}})
	if err != nil || res.IsError {
		t.Fatalf("context: %v %v", err, res)
	}
	text := toolPayload(t, res)
	if ran != 0 {
		t.Fatalf("the context tool ran %d witness runs under records that fail hygiene", ran)
	}
	if strings.Contains(text, `"bucket":"BUCKET_BROKEN"`) || strings.Contains(text, "unwitnessed") {
		t.Fatalf("the record-only pass judged REQ-m-a against absent witness evidence:\n%s", text)
	}
}
