package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/stipulate"
	"github.com/spf13/cobra"
)

// scaffoldCorpus initializes a corpus under a fresh chdir for an
// in-process verb and returns a writer for its files; the backend seam
// is restored with the directory for the callers that replace it.
func scaffoldCorpus(t *testing.T) func(path, content string) {
	t.Helper()
	priorDir, priorMake := chdir, makeBackends
	chdir = t.TempDir()
	t.Cleanup(func() { chdir, makeBackends = priorDir, priorMake })
	scaffold := initCmd()
	scaffold.SetArgs([]string{})
	if err := scaffold.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	return func(path, content string) {
		t.Helper()
		full := filepath.Join(chdir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// The ids form of pin is all-or-nothing on the CLI as on the MCP: a
// refusal mid-list (an id outside the corpus) writes nothing — the
// stale pins the earlier ids would have re-consented stay stale
// (REQ-change-editorial).
func TestPinCLIIdsFormIsAllOrNothing(t *testing.T) {
	stipulate.Covers(t, "REQ-change-editorial")
	if testing.Short() {
		t.Skip("compiles a corpus")
	}
	write := scaffoldCorpus(t)
	stale := strings.Repeat("0", 64)
	write("docs/specs/s.md", "# S\n\n**REQ-pa-a** (behavior): It MUST a.\n\n**REQ-pa-b** (behavior): It MUST b.\n")
	bindings := "bindings {\n  requirement_id: \"REQ-pa-a\"\n  content_hash: \"" + stale + "\"\n  backend: \"go\"\n  symbol: \"example.com/p.A\"\n  role: BINDING_ROLE_IMPLEMENTS\n}\n" +
		"bindings {\n  requirement_id: \"REQ-pa-b\"\n  content_hash: \"" + stale + "\"\n  backend: \"go\"\n  symbol: \"example.com/p.B\"\n  role: BINDING_ROLE_IMPLEMENTS\n}\n"
	write(".stipulator/bindings/b.textproto", bindings)
	built := 0
	makeBackends = func(context.Context, string) (map[string]verify.Backend, func() error, error) {
		built++
		backend := &recordingBackend{}
		return map[string]verify.Backend{"go": backend}, backend.Close, nil
	}
	cmd := pinCmd()
	cmd.SetArgs([]string{"--req", "REQ-pa-a", "--req", "REQ-pa-b", "--req", "REQ-pa-absent"})
	err := cmd.ExecuteContext(context.Background())
	if err == nil || !strings.Contains(err.Error(), "REQ-pa-absent") {
		t.Fatalf("pin with an unknown id = %v; want the refusal naming it", err)
	}
	// The judgment fires before any resolver is spawned: a refused
	// batch built no backend (REQ-check-preparation).
	if built != 0 {
		t.Fatalf("a refused ids form built %d backend(s) before refusing", built)
	}
	after, readErr := os.ReadFile(filepath.Join(chdir, ".stipulator", "bindings", "b.textproto"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(after) != bindings {
		t.Fatalf("a refused ids form wrote the earlier ids' pins:\n%s", after)
	}
	cmd = pinCmd()
	cmd.SetArgs([]string{"--req", "REQ-pa-a", "--req", "REQ-pa-b"})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("the same ids without the stranger = %v", err)
	}
	if after, _ = os.ReadFile(filepath.Join(chdir, ".stipulator", "bindings", "b.textproto")); strings.Contains(string(after), stale) {
		t.Fatalf("the accepted ids form left a stale pin:\n%s", after)
	}
}

// An identifier list given but reducing to nothing refuses on the CLI
// as on the MCP — it never runs the global pass
// (REQ-check-preparation).
func TestCheckIdsListReducingToNothingRefuses(t *testing.T) {
	stipulate.Covers(t, "REQ-check-preparation")
	if testing.Short() {
		t.Skip("compiles a corpus")
	}
	scaffoldCorpus(t)
	for _, verb := range []struct {
		name   string
		newCmd func() *cobra.Command
		flag   string
	}{{"check", checkCmd, "--ids"}, {"verify", verifyCmd, "--req"}, {"gate", gateCmd, "--req"}} {
		cmd := verb.newCmd()
		cmd.SetArgs([]string{verb.flag, ","})
		err := cmd.ExecuteContext(context.Background())
		if err == nil || !strings.Contains(err.Error(), "no requirement identifiers given") {
			t.Fatalf("%s %s \",\" = %v; want the empty-reduction refusal, never the global pass", verb.name, verb.flag, err)
		}
	}
}
