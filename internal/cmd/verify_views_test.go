package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"google.golang.org/protobuf/encoding/protojson"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gofresh "github.com/greatliontech/gofresh"
	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/backends/golang"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/internal/wire"
	"github.com/greatliontech/stipulator/stipulate"
)

// captureStdout runs fn with os.Stdout redirected and returns what it
// printed: the CLI verbs print through fmt.Printf, so a rendering test
// reads the process's own stdout.
func captureStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	prior := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	done := make(chan string)
	go func() {
		var b bytes.Buffer
		_, _ = io.Copy(&b, r)
		done <- b.String()
	}()
	runErr := fn()
	os.Stdout = prior
	_ = w.Close()
	return <-done, runErr
}

// The CLI verify serves the bindings view the MCP serves — one row per
// claim, scoped by --req / --filter / --path, as text or JSON — so
// "what claims this symbol" is a query at the shell too; and its
// vocabulary refuses before any witness executes
// (REQ-mcp-surfaces, REQ-check-preparation).
func TestVerifyBindingsViewAnswersWhatClaimsThisSymbol(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-surfaces", "REQ-check-preparation", "REQ-mcp-tools")
	if testing.Short() {
		t.Skip("executes a race-instrumented policy over a fixture tree")
	}
	neutralAmbient(t)
	dir := t.TempDir()
	// The oracle for "did a witness run" is in-process: the verb's one
	// witnessing entry is counted, not a file a child test writes (a
	// runtime input outside every bracket, which would keep this
	// witness from ever serving fresh).
	witnessed := 0
	prior := runWitnessesPolicy
	runWitnessesPolicy = func(ctx context.Context, pc *golang.Capture, seeding verify.WitnessSeeding) (*verify.TestRun, error) {
		witnessed++
		return prior(ctx, pc, seeding)
	}
	t.Cleanup(func() { runWitnessesPolicy = prior })
	files := map[string]string{
		"go.mod":                         "module example.com/verifyfix\n\ngo 1.26.4\n",
		"ok/ok.go":                       "package ok\n\nfunc Double(x int) int { return 2 * x }\n\nfunc Round(x int) int { return x }\n",
		"ok/ok_test.go":                  "package ok\n\nimport \"testing\"\n\nfunc TestDouble(t *testing.T) {\n\tif Double(2) != 4 {\n\t\tt.Fatal(\"broken\")\n\t}\n}\n",
		"specs/check.md":                 "# Check\n\n**REQ-fix-a** (behavior): The fixture MUST double.\n\n**REQ-fix-b** (behavior): The fixture MUST round.\n",
		".stipulator/manifest.textproto": "include: \"specs/**/*.md\"\n",
		".stipulator/policy.textproto":   "invocations {\n  name: \"all\"\n  timeout {\n    seconds: 300\n  }\n  go {\n    packages: \"./...\"\n    race: true\n  }\n}\n",
		".stipulator/bindings/fix.textproto": "bindings {\n  requirement_id: \"REQ-fix-a\"\n  backend: \"go\"\n  symbol: \"example.com/verifyfix/ok.TestDouble\"\n  role: BINDING_ROLE_TESTS\n}\n" +
			"bindings {\n  requirement_id: \"REQ-fix-b\"\n  backend: \"go\"\n  symbol: \"example.com/verifyfix/ok.Round\"\n  role: BINDING_ROLE_IMPLEMENTS\n}\n",
	}
	for path, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	priorDir := chdir
	chdir = dir
	t.Cleanup(func() { chdir = priorDir })
	executed := func() bool { return witnessed > 0 }

	// Vocabulary refuses before any witness executes.
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--view", "bogus"}, "unknown view"},
		{[]string{"--req", "REQ-fix-nope"}, "REQ-fix-nope"},
		{[]string{"--filter", "["}, "bad filter"},
	} {
		cmd := verifyCmd()
		cmd.SetArgs(tc.args)
		err := cmd.ExecuteContext(context.Background())
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%v: err = %v, want a refusal naming %q", tc.args, err, tc.want)
		}
		if executed() {
			t.Fatalf("%v: a witness executed under a refused vocabulary", tc.args)
		}
	}

	// The pre-deletion query: what claims Round? One row, records-only.
	out, err := captureStdout(t, func() error {
		cmd := verifyCmd()
		cmd.SetArgs([]string{"--no-test", "--view", "bindings", "--path", "example.com/verifyfix/ok.Round"})
		return cmd.ExecuteContext(context.Background())
	})
	if err != nil {
		t.Fatalf("verify --view bindings --path: %v\n%s", err, out)
	}
	if !strings.Contains(out, "REQ-fix-b  implements example.com/verifyfix/ok.Round") || strings.Contains(out, "TestDouble") || !strings.Contains(out, "1 binding row(s)") {
		t.Fatalf("bindings view by path:\n%s", out)
	}
	if executed() {
		t.Fatal("--no-test executed a witness")
	}
	// Unwitnessed, a tests-role row carries no outcome column at all:
	// "not run" would misreport the records-only judgment.
	out, err = captureStdout(t, func() error {
		cmd := verifyCmd()
		cmd.SetArgs([]string{"--no-test", "--view", "bindings", "--req", "REQ-fix-a"})
		return cmd.ExecuteContext(context.Background())
	})
	if err != nil || strings.Contains(out, "not_run") || strings.Contains(out, "unwitnessed") || strings.Contains(out, "passed") {
		t.Fatalf("unwitnessed bindings view carries an outcome: %v\n%s", err, out)
	}
	// A scope narrows the summary too: --path on the default view
	// counts that path's claims, not the tree's.
	out, err = captureStdout(t, func() error {
		cmd := verifyCmd()
		cmd.SetArgs([]string{"--no-test", "--path", "example.com/verifyfix/ok.Round"})
		return cmd.ExecuteContext(context.Background())
	})
	if err != nil || !strings.Contains(out, "claims:    1 bindings") {
		t.Fatalf("scoped summary: %v\n%s", err, out)
	}
	// The same query as JSON is the MCP's VerifyReport projection.
	out, err = captureStdout(t, func() error {
		cmd := verifyCmd()
		cmd.SetArgs([]string{"--no-test", "--view", "bindings", "--req", "REQ-fix-a", "--json"})
		return cmd.ExecuteContext(context.Background())
	})
	if err != nil {
		t.Fatalf("verify --json: %v\n%s", err, out)
	}
	var rep struct {
		Results []struct {
			RequirementId string `json:"requirementId"`
			Symbol        string `json:"symbol"`
		} `json:"results"`
	}
	// The bytes are the one canonical projection of a strict
	// VerifyReport — never a raw marshal (REQ-mcp-tools).
	decoded := &stipulatorv1.VerifyReport{}
	if perr := protojson.Unmarshal([]byte(out), decoded); perr != nil {
		t.Fatalf("verify --json is not a strict VerifyReport: %v\n%s", perr, out)
	}
	if canonical, cerr := wire.CanonicalJSON(decoded); cerr != nil || out != string(canonical) {
		t.Fatalf("verify --json is not the canonical projection (%v):\n%s", cerr, out)
	}
	if jerr := json.Unmarshal([]byte(out), &rep); jerr != nil || len(rep.Results) != 1 || rep.Results[0].RequirementId != "REQ-fix-a" || !strings.HasSuffix(rep.Results[0].Symbol, "TestDouble") {
		t.Fatalf("json bindings view scoped by --req: %v\n%s", jerr, out)
	}
	// The default view is unchanged: the operator's counts.
	out, err = captureStdout(t, func() error {
		cmd := verifyCmd()
		cmd.SetArgs([]string{"--no-test"})
		return cmd.ExecuteContext(context.Background())
	})
	if err != nil || !strings.Contains(out, "claims:    2 bindings") {
		t.Fatalf("summary: %v\n%s", err, out)
	}
	// Positive control for the vocabulary oracle: a witnessed bindings
	// view executes the test and reports its outcome on the row.
	out, err = captureStdout(t, func() error {
		cmd := verifyCmd()
		cmd.SetArgs([]string{"--view", "bindings", "--req", "REQ-fix-a"})
		return cmd.ExecuteContext(context.Background())
	})
	if err != nil || !executed() || !strings.Contains(out, "passed") {
		t.Fatalf("witnessed bindings view: %v executed=%v\n%s", err, executed(), out)
	}
}

// The CLI explain takes the reason, or the package and symbol, exactly
// as the MCP tool does — a lone package or symbol refuses, an
// unparseable reason refuses naming the alternative
// (REQ-mcp-surfaces).
//
//gofresh:pure
func TestExplainArgumentContract(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-surfaces")
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--package", "example.com/p"}, "travel together"},
		{[]string{"--symbol", "V"}, "travel together"},
		// An explicit empty list: nil would hand cobra the process's own
		// arguments.
		{[]string{}, "pass --reason to parse, or --package and --symbol"},
		{[]string{"--reason", "nothing parseable here"}, "no culprit parsed"},
	} {
		cmd := explainCmd()
		cmd.SetArgs(tc.args)
		err := cmd.ExecuteContext(context.Background())
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%v: err = %v, want %q", tc.args, err, tc.want)
		}
	}
}

// The CLI explain prints the same links the MCP's structured result
// carries — one per line with kind, culprit, callee, clause, and
// position — with the arm and view in the header and the omitted count
// on stderr; a culprit no view knows says so (REQ-mcp-explain).
//
//gofresh:pure
func TestExplainRendersTheChain(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-explain")
	chain := gofresh.Chain{
		Arm: "environment-audit",
		Links: []gofresh.ChainLink{
			{Kind: "edge", Package: "example.com/reg", Symbol: "Registry", Callee: "gen", Clause: "a binding source refused", Pos: "reg.go:12"},
			{Kind: "refusal", Package: "example.com/reg", Symbol: "gen", Clause: "a stored value refused", Pos: "reg.go:7"},
		},
		Omitted: 3,
	}
	prior := explainChain
	var gotPkg, gotSym string
	explainChain = func(_ context.Context, _, pkgPath, symbol string) (gofresh.Chain, string, error) {
		gotPkg, gotSym = pkgPath, symbol
		if symbol == "Missing" {
			return gofresh.Chain{}, "", nil
		}
		return chain, "race", nil
	}
	t.Cleanup(func() { explainChain = prior })
	out, err := captureStdout(t, func() error {
		cmd := explainCmd()
		cmd.SetArgs([]string{"--reason", "example.com/reg: example.com/reg.Registry escapes writable"})
		return cmd.ExecuteContext(context.Background())
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotPkg != "example.com/reg" || gotSym != "Registry" {
		t.Fatalf("culprit parsed from the reason = %q %q", gotPkg, gotSym)
	}
	for _, want := range []string{
		"explain: example.com/reg.Registry — environment-audit (view: race)",
		"   1  edge     example.com/reg.Registry  → gen  [a binding source refused]  reg.go:12",
		"   2  refusal  example.com/reg.gen  [a stored value refused]  reg.go:7",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("rendering lacks %q:\n%s", want, out)
		}
	}
	out, err = captureStdout(t, func() error {
		cmd := explainCmd()
		cmd.SetArgs([]string{"--package", "example.com/reg", "--symbol", "Missing"})
		return cmd.ExecuteContext(context.Background())
	})
	if err != nil || !strings.Contains(out, "explain: no chain — example.com/reg.Missing is not a culprit in the policy views") {
		t.Fatalf("empty chain: %v\n%s", err, out)
	}
}
