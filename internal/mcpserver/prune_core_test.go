package mcpserver

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/greatliontech/gofresh"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/greatliontech/stipulator/internal/backends/golang"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/stipulate"
)

// resolveRefusingBackend answers every resolution with an error — the
// run-time verification problem only a backend can raise.
type resolveRefusingBackend struct{}

func (resolveRefusingBackend) Resolve(string) (verify.Resolution, string, error) {
	return verify.NotFound, "", errors.New("resolver unavailable")
}

// A verification problem the witness run's backend raises refuses the
// resolved-record evaluation rather than deleting on a shaky reading:
// the refusal names the problem and the gap record stays
// (REQ-gap-resolved-pruned).
func TestPruneRefusesOnAVerificationProblem(t *testing.T) {
	stipulate.Covers(t, "REQ-gap-resolved-pruned")
	gapPath := ".stipulator/gaps/m-a.textproto"
	sess, writes := harnessWith(t, map[string]string{
		".stipulator/bindings/a.textproto": pinnedBindingFor(t, "REQ-m-a", "example.com/p.TestA", "s"),
		gapPath: "requirement_id: \"REQ-m-a\"\nreason: \"pending\"\n" +
			"lands {\n  manual {\n    condition: \"judged done\"\n    fired: true\n  }\n}\n",
	}, func(s *Server) {
		s.backends = func(context.Context, []string) (map[string]verify.Backend, error) {
			return map[string]verify.Backend{"go": resolveRefusingBackend{}}, nil
		}
		s.runTests = func(context.Context, *golang.Capture, verify.WitnessSeeding, map[gofresh.Subject]bool) (*verify.TestRun, error) {
			return &verify.TestRun{
				RaceEnabled:      true,
				SelectiveServing: true,
				Outcomes:         map[string]verify.TestOutcome{"example.com/p.TestA": verify.TestPassed},
			}, nil
		}
	})
	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "prune", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatalf("prune under a verification problem succeeded: %+v", res)
	}
	if text := toolText(t, res); !strings.Contains(text, "verification problems") || !strings.Contains(text, "resolver unavailable") {
		t.Fatalf("refusal does not name the problem: %s", text)
	}
	if _, wrote := writes[gapPath]; wrote {
		t.Fatal("refused prune touched the gap record")
	}
}
