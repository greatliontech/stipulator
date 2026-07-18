package cmd

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/greatliontech/stipulator/internal/policy"
	"github.com/greatliontech/stipulator/stipulate"
)

// runPolicyInit executes `policy init` against dir, capturing stdout.
func runPolicyInit(t *testing.T, dir string) (string, error) {
	t.Helper()
	priorDir := chdir
	chdir = dir
	t.Cleanup(func() { chdir = priorDir })
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	priorStdout := os.Stdout
	os.Stdout = write
	cmd := policyInitCmd()
	cmd.SetArgs([]string{})
	runErr := cmd.ExecuteContext(context.Background())
	os.Stdout = priorStdout
	if err := write.Close(); err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(read)
	if err != nil {
		t.Fatal(err)
	}
	return string(out), runErr
}

// TestPolicyInitIsIdempotentAndRefusesDivergence pins the migration
// command end to end: it writes the derived record only when absent,
// states the configuration break, is a no-op on an identical record, and
// refuses a diverging record — stating the divergence — without ever
// rewriting it.
func TestPolicyInitIsIdempotentAndRefusesDivergence(t *testing.T) {
	stipulate.Covers(t, "REQ-policy-explicit")
	stipulate.Covers(t, "REQ-policy-record-location")
	stipulate.Covers(t, "REQ-policy-init-immutable")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/policyfixture\n\ngo 1.26.4\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	full := filepath.Join(dir, filepath.FromSlash(policy.Path))

	out, err := runPolicyInit(t, dir)
	if err != nil {
		t.Fatalf("policy init: %v\n%s", err, out)
	}
	if !strings.Contains(out, "configuration break") || !strings.Contains(out, "reviewed test policy") {
		t.Errorf("output does not state the configuration break:\n%s", out)
	}
	written, err := os.ReadFile(full)
	if err != nil {
		t.Fatalf("record not written: %v", err)
	}
	if p, err := policy.Parse(written); err != nil {
		t.Fatalf("written record does not strict-parse: %v", err)
	} else if len(p.GetInvocations()) != 1 || p.GetInvocations()[0].GetName() != "race" {
		t.Fatalf("derived record = %s", written)
	}

	out, err = runPolicyInit(t, dir)
	if err != nil {
		t.Fatalf("second policy init: %v", err)
	}
	if !strings.Contains(out, "nothing to do") {
		t.Errorf("second run is not reported as a no-op:\n%s", out)
	}
	after, err := os.ReadFile(full)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, written) {
		t.Error("idempotent rerun changed the record")
	}

	divergent := bytes.Replace(written, []byte("seconds: 7200"), []byte("seconds: 900"), 1)
	if bytes.Equal(divergent, written) {
		t.Fatal("fixture edit did not diverge the record")
	}
	if err := os.WriteFile(full, divergent, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = runPolicyInit(t, dir)
	if err == nil {
		t.Fatal("divergent record accepted")
	}
	for _, part := range []string{"diverges", "never rewritten", `"    seconds: 900"`} {
		if !strings.Contains(err.Error(), part) {
			t.Errorf("error = %q, want it to contain %q", err, part)
		}
	}
	after, err = os.ReadFile(full)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, divergent) {
		t.Error("refusal rewrote the existing record")
	}
}

// TestPolicyInitRoutesThroughRootCommand pins the command wiring: `stipulator
// policy init` reaches the derivation through Execute, and the root `init`
// command's corpus-free path stays distinct from it.
func TestPolicyInitRoutesThroughRootCommand(t *testing.T) {
	stipulate.Covers(t, "REQ-policy-init-immutable")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	scaffold := newRootCmd()
	scaffold.SetArgs([]string{"--chdir", dir, "init"})
	if err := scaffold.Execute(); err != nil {
		t.Fatalf("scaffold corpus: %v", err)
	}
	root := newRootCmd()
	root.SetArgs([]string{"--chdir", dir, "policy", "init"})
	if err := root.Execute(); err != nil {
		t.Fatalf("policy init via root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".stipulator", "policy.textproto")); err != nil {
		t.Fatalf("policy record not written: %v", err)
	}
	// Second execution is the byte-identical no-op.
	again := newRootCmd()
	again.SetArgs([]string{"--chdir", dir, "policy", "init"})
	if err := again.Execute(); err != nil {
		t.Fatalf("idempotent rerun: %v", err)
	}
}
