package cmd

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/stipulate"
)

// stderrOf runs fn with os.Stderr captured and returns what it wrote.
func stderrOf(t *testing.T, fn func()) string {
	t.Helper()
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	prior := os.Stderr
	os.Stderr = write
	fn()
	os.Stderr = prior
	if err := write.Close(); err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(read)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// Every CLI verb that builds the serving form renders its account on
// stderr, read after the close that publishes
// (REQ-evidence-resolution-freshness-account): the first verb publishes the
// bound symbols' resolution records and says so; the next serves them
// and says that.
func TestServingVerbsRenderTheResolutionAccount(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-resolution-freshness-account")
	if testing.Short() {
		t.Skip("resolves a fixture module's symbols through the served backend")
	}
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":                             "module example.com/account\n\ngo 1.26.4\n",
		"ok/ok.go":                           "package ok\n\nfunc Double(x int) int { return 2 * x }\n",
		"ok/ok_test.go":                      "package ok\n\nimport \"testing\"\n\nfunc TestDouble(t *testing.T) { Double(2) }\n",
		"specs/a.md":                         "# A\n\n**REQ-acc-dbl** (behavior): The fixture MAY double.\n\n**REQ-acc-may** (behavior): The fixture MAY triple.\n",
		".stipulator/manifest.textproto":     "include: \"specs/**/*.md\"\n",
		".stipulator/bindings/a.textproto":   "bindings {\n  requirement_id: \"REQ-acc-dbl\"\n  backend: \"go\"\n  symbol: \"example.com/account/ok.Double\"\n  role: BINDING_ROLE_IMPLEMENTS\n}\n",
		".stipulator/gaps/acc-may.textproto": "requirement_id: \"REQ-acc-may\"\nreason: \"pending\"\nlands {\n  manual {\n    condition: \"judged done\"\n  }\n}\n",
		".stipulator/policy.textproto":       "invocations {\n  name: \"all\"\n  timeout {\n    seconds: 300\n  }\n  go {\n    packages: \"./...\"\n    race: true\n  }\n}\n",
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
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("NO_COLOR", "1")
	priorDir := chdir
	chdir = dir
	t.Cleanup(func() { chdir = priorDir })
	for i, tc := range []struct {
		name string
		cmd  *cobra.Command
		args []string
	}{{"verify", verifyCmd(), []string{"--no-test"}}, {"prune", pruneCmd(), []string{"--no-test", "--check"}}, {"verify --json", verifyCmd(), []string{"--no-test", "--json"}}, {"gap --list", gapCmd(), []string{"--list"}}, {"gate", gateCmd(), []string{}}} {
		tc.cmd.SetArgs(tc.args)
		var runErr error
		errOut := stderrOf(t, func() {
			priorStdout := os.Stdout
			devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
			if err != nil {
				t.Fatal(err)
			}
			os.Stdout = devNull
			runErr = tc.cmd.ExecuteContext(context.Background())
			os.Stdout = priorStdout
			devNull.Close()
		})
		// gate's verdict is its exit status — the fixture's unwitnessed
		// requirement is red — and the account renders before it; a
		// refusal is any other error.
		var verdict ExitStatus
		if runErr != nil && !(tc.name == "gate" && errors.As(runErr, &verdict)) {
			t.Fatalf("%s: %v\n%s", tc.name, runErr, errOut)
		}
		if !strings.Contains(errOut, "resolution: ") {
			t.Fatalf("%s rendered no serving account:\n%s", tc.name, errOut)
		}
		if i == 0 && !strings.Contains(errOut, "resolution published under \"default\": 1 record") {
			t.Fatalf("the first verb's account names no publish:\n%s", errOut)
		}
		if i > 0 && !strings.Contains(errOut, "resolution: 1 served from records") {
			t.Fatalf("%s served nothing from the records the first verb published:\n%s", tc.name, errOut)
		}
	}
	// gate --quiet is exit code only: the account is suppressed with the
	// rest of the human rendering.
	quietGate := gateCmd()
	quietGate.SetArgs([]string{"--quiet"})
	var runErr error
	errOut := stderrOf(t, func() { runErr = quietGate.ExecuteContext(context.Background()) })
	var verdict ExitStatus
	if runErr != nil && !errors.As(runErr, &verdict) {
		t.Fatalf("gate --quiet: %v\n%s", runErr, errOut)
	}
	if strings.Contains(errOut, "resolution: ") {
		t.Fatalf("gate --quiet rendered the account:\n%s", errOut)
	}
}

// faultingAccountBackend resolves one symbol with an error — the
// verification problem a pass raises after its backends published —
// and carries an account whose publish line exists only after Close.
type faultingAccountBackend struct {
	closed bool
}

func (b *faultingAccountBackend) Resolve(symbol string) (verify.Resolution, string, error) {
	if symbol == "example.com/account/ok.Double" {
		return verify.NotFound, "", errors.New("resolver unavailable")
	}
	return verify.Resolved, strings.Repeat("a", 64), nil
}

func (b *faultingAccountBackend) Close() error {
	b.closed = true
	return nil
}

// NeverServe is the seeding classification the witness run asks of the
// Go backend: nothing refused.
func (b *faultingAccountBackend) NeverServe([]string) (map[string]string, error) {
	return map[string]string{}, nil
}

func (b *faultingAccountBackend) Notices() []string {
	out := []string{"resolution: 0 served from records, 1 resolved typed"}
	if b.closed {
		out = append(out, "resolution published under \"default\": 1 record")
	}
	return out
}

// A refusal the CLI gate and prune raise after the close that published
// renders the account before the problems — and gate --quiet renders
// neither the account nor anything else of the human form
// (REQ-evidence-resolution-freshness-account).
func TestServingRefusalsRenderTheResolutionAccount(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-resolution-freshness-account")
	if testing.Short() {
		t.Skip("executes a race invocation over a fixture module")
	}
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":                             "module example.com/account\n\ngo 1.26.4\n",
		"ok/ok.go":                           "package ok\n\nfunc Double(x int) int { return 2 * x }\n",
		"ok/ok_test.go":                      "package ok\n\nimport \"testing\"\n\nfunc TestDouble(t *testing.T) { Double(2) }\n",
		"specs/a.md":                         "# A\n\n**REQ-acc-dbl** (behavior): The fixture MAY double.\n\n**REQ-acc-may** (behavior): The fixture MAY triple.\n",
		".stipulator/manifest.textproto":     "include: \"specs/**/*.md\"\n",
		".stipulator/bindings/a.textproto":   "bindings {\n  requirement_id: \"REQ-acc-dbl\"\n  backend: \"go\"\n  symbol: \"example.com/account/ok.Double\"\n  role: BINDING_ROLE_IMPLEMENTS\n}\n",
		".stipulator/gaps/acc-may.textproto": "requirement_id: \"REQ-acc-may\"\nreason: \"pending\"\nlands {\n  manual {\n    condition: \"judged done\"\n  }\n}\n",
		".stipulator/policy.textproto":       "invocations {\n  name: \"all\"\n  timeout {\n    seconds: 300\n  }\n  go {\n    packages: \"./...\"\n    race: true\n  }\n}\n",
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
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("NO_COLOR", "1")
	priorDir, priorServing := chdir, servingBackends
	chdir = dir
	servingBackends = func(context.Context, string, []string) (map[string]verify.Backend, error) {
		return map[string]verify.Backend{"go": &faultingAccountBackend{}}, nil
	}
	t.Cleanup(func() { chdir, servingBackends = priorDir, priorServing })
	for _, tc := range []struct {
		name  string
		cmd   *cobra.Command
		args  []string
		quiet bool
	}{{"gate", gateCmd(), []string{}, false}, {"prune", pruneCmd(), []string{"--no-test", "--check"}, false}, {"gate --quiet", gateCmd(), []string{"--quiet"}, true}} {
		tc.cmd.SetArgs(tc.args)
		var runErr error
		errOut := stderrOf(t, func() {
			priorStdout := os.Stdout
			devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
			if err != nil {
				t.Fatal(err)
			}
			os.Stdout = devNull
			runErr = tc.cmd.ExecuteContext(context.Background())
			os.Stdout = priorStdout
			devNull.Close()
		})
		if runErr == nil || !strings.Contains(runErr.Error(), "verification problems") {
			t.Fatalf("%s over a resolution fault returned %v; want the problems refusal\n%s", tc.name, runErr, errOut)
		}
		if tc.quiet {
			if strings.Contains(errOut, "resolution: ") {
				t.Fatalf("%s rendered the account:\n%s", tc.name, errOut)
			}
			continue
		}
		account := strings.Index(errOut, "resolution published under \"default\": 1 record")
		problem := strings.Index(errOut, "resolver unavailable")
		if account < 0 || problem < 0 || account > problem {
			t.Fatalf("%s refusal does not render the account before the problem:\n%s", tc.name, errOut)
		}
	}
}
