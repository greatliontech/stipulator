package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/greatliontech/gofresh"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/greatliontech/stipulator/internal/backends/golang"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/stipulate"
)

// accountBackend resolves like fakeBackend and carries the serving path's
// account, the publish line present only after its close.
type accountBackend struct {
	shapes fakeBackend
	closed bool
}

func (b *accountBackend) Resolve(symbol string) (verify.Resolution, string, error) {
	return b.shapes.Resolve(symbol)
}

func (b *accountBackend) Close() error {
	b.closed = true
	return nil
}

func (b *accountBackend) Notices() []string {
	out := []string{"resolution: 0 served from records, 4 resolved typed", "resolution typed: example.com/p.F: no record"}
	if b.closed {
		out = append(out, "resolution published under \"default\": 4 records")
	}
	return out
}

// accountServer is the harness's server with an accounting backend.
func accountServer(t *testing.T, files map[string]string) *mcp.ClientSession {
	t.Helper()
	sess, _ := harnessWith(t, files, func(s *Server) {
		s.backends = func(context.Context, []string) (map[string]verify.Backend, error) {
			return map[string]verify.Backend{"go": &accountBackend{shapes: fakeBackend{
				"example.com/p.TestA": strings.Repeat("s", 64),
				"example.com/p.TestB": strings.Repeat("b", 64),
				"example.com/p.F":     strings.Repeat("f", 64),
				"example.com/q.TestA": strings.Repeat("q", 64),
			}}}, nil
		}
		s.runTests = func(context.Context, *golang.Capture, verify.WitnessSeeding, map[gofresh.Subject]bool) (*verify.TestRun, error) {
			return &verify.TestRun{RaceEnabled: true, SelectiveServing: true, Outcomes: map[string]verify.TestOutcome{"example.com/p.TestA": verify.TestPassed}}, nil
		}
	})
	return sess
}

// Every served verb that builds the serving form carries its account
// (REQ-evidence-resolution-freshness, REQ-mcp-response-contract): the
// text digest of verify and gate carries the account read after the
// close — the publish line included, the per-symbol typed lines left to
// the structured payload — the full verify report carries every line,
// and prune's notes carry the account beside the evaluation line.
func TestServedVerbsCarryTheServingAccount(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-resolution-freshness", "REQ-mcp-response-contract")
	binding := pinnedBindingFor(t, "REQ-m-a", "example.com/p.TestA", "s")
	gap := "requirement_id: \"REQ-m-b\"\nreason: \"pending\"\nlands {\n  manual {\n    condition: \"judged done\"\n  }\n}\n"
	sess := accountServer(t, map[string]string{
		".stipulator/bindings/a.textproto": binding,
		".stipulator/gaps/m-b.textproto":   gap,
	})
	ctx := context.Background()
	for _, tool := range []string{"verify", "gate"} {
		res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: map[string]any{}})
		if err != nil {
			t.Fatal(err)
		}
		if res.IsError {
			t.Fatalf("%s refused: %s", tool, toolText(t, res))
		}
		text := toolText(t, res)
		if !strings.Contains(text, "resolution published under \"default\": 4 records") || !strings.Contains(text, "resolution: 0 served from records") {
			t.Fatalf("%s digest carries no account read after the close:\n%s", tool, text)
		}
		if strings.Contains(text, "resolution typed: ") {
			t.Fatalf("%s digest carries a per-symbol typed line:\n%s", tool, text)
		}
	}
	res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: "verify", Arguments: map[string]any{"view": "bindings"}})
	if err != nil {
		t.Fatal(err)
	}
	full, _ := json.Marshal(res.StructuredContent)
	if !strings.Contains(string(full), "resolution published under") || !strings.Contains(string(full), "resolution typed: example.com/p.F") {
		t.Fatalf("the full verify report drops part of the account: %s", full)
	}
	for _, args := range []map[string]any{{"check": true}, {}} {
		res, err = sess.CallTool(ctx, &mcp.CallToolParams{Name: "prune", Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		if res.IsError {
			t.Fatalf("prune %v refused: %s", args, toolText(t, res))
		}
		notes, _ := json.Marshal(res.StructuredContent)
		if !strings.Contains(string(notes), "evaluated 1 gap records") || !strings.Contains(string(notes), "resolution published under") || strings.Contains(string(notes), "resolution typed: ") {
			t.Fatalf("prune %v: the notes carry no account beside the evaluation, or a per-symbol typed line: %s", args, notes)
		}
	}
	// The gap list, context and partitions build the serving form too:
	// their digests carry the account by the served rule, the gap
	// list's notes the same bounded account.
	for _, call := range []struct {
		tool string
		args map[string]any
	}{{"gap", map[string]any{"list": true}}, {"context", map[string]any{"ids": "REQ-m-a"}}, {"partitions", map[string]any{}}} {
		res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: call.tool, Arguments: call.args})
		if err != nil {
			t.Fatal(err)
		}
		if res.IsError {
			t.Fatalf("%s refused: %s", call.tool, toolText(t, res))
		}
		text := toolText(t, res)
		if !strings.Contains(text, "resolution published under \"default\": 4 records") || strings.Contains(text, "resolution typed: ") {
			t.Fatalf("%s digest = %q; want the account by the digest's rule", call.tool, text)
		}
		if call.tool == "gap" {
			if notes, _ := json.Marshal(res.StructuredContent); !strings.Contains(string(notes), "resolution published under") || strings.Contains(string(notes), "resolution typed: ") {
				t.Fatalf("the gap list's notes carry no account, or a per-symbol typed line: %s", notes)
			}
		}
	}
}

// refusingAccountBackend is accountBackend with one symbol whose
// resolution faults — the verification problem a serving pass raises
// after its backends have published.
type refusingAccountBackend struct {
	accountBackend
}

func (b *refusingAccountBackend) Resolve(symbol string) (verify.Resolution, string, error) {
	if symbol == "example.com/q.TestA" {
		return verify.NotFound, "", errors.New("resolver unavailable")
	}
	return b.accountBackend.Resolve(symbol)
}

// A refusal a served verb raises after the close that published carries
// the account — gate's and partitions' problem refusals, prune's
// evaluation refusal (REQ-evidence-resolution-freshness).
func TestServedRefusalsAfterTheCloseCarryTheAccount(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-resolution-freshness")
	gap := "requirement_id: \"REQ-m-b\"\nreason: \"pending\"\nlands {\n  manual {\n    condition: \"judged done\"\n  }\n}\n"
	sess, _ := harnessWith(t, map[string]string{
		".stipulator/bindings/a.textproto": pinnedBindingFor(t, "REQ-m-a", "example.com/p.TestA", "s"),
		".stipulator/bindings/q.textproto": pinnedBindingFor(t, "REQ-m-b", "example.com/q.TestA", "q"),
		".stipulator/gaps/m-b.textproto":   gap,
	}, func(s *Server) {
		s.backends = func(context.Context, []string) (map[string]verify.Backend, error) {
			return map[string]verify.Backend{"go": &refusingAccountBackend{accountBackend{shapes: fakeBackend{
				"example.com/p.TestA": strings.Repeat("s", 64),
				"example.com/q.TestA": strings.Repeat("q", 64),
			}}}}, nil
		}
		s.runTests = func(context.Context, *golang.Capture, verify.WitnessSeeding, map[gofresh.Subject]bool) (*verify.TestRun, error) {
			return &verify.TestRun{RaceEnabled: true, SelectiveServing: true, Outcomes: map[string]verify.TestOutcome{"example.com/p.TestA": verify.TestPassed}}, nil
		}
	})
	ctx := context.Background()
	for _, call := range []struct {
		tool string
		args map[string]any
	}{{"gate", map[string]any{}}, {"partitions", map[string]any{}}, {"prune", map[string]any{"check": true}}} {
		res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: call.tool, Arguments: call.args})
		if err != nil {
			t.Fatal(err)
		}
		if !res.IsError {
			t.Fatalf("%s over a resolution fault did not refuse", call.tool)
		}
		text := toolText(t, res)
		if !strings.Contains(text, "resolver unavailable") || !strings.Contains(text, "resolution published under \"default\": 4 records") || strings.Contains(text, "resolution typed: ") {
			t.Fatalf("%s refusal = %q; want the problem and the account by the digest's rule", call.tool, text)
		}
	}
}
