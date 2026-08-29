package cmd

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/greatliontech/stipulator/stipulate"
)

// The blanket pin backfills the unset pin, refuses to rewrite the
// differing one, and names the refused requirement in its own output —
// the caller learns what awaits re-consent from pin, not from a later
// staleness report. Naming the requirement with --req is the re-consent
// that rewrites it, after which the blanket form reports quiescence
// without the awaiting line.
//
// Deliberately not //gofresh:pure: builds and executes the CLI binary.
func TestPinCLINamesPreservedDifferingPins(t *testing.T) {
	stipulate.Covers(t, "REQ-pin-backfill")
	if testing.Short() {
		t.Skip("builds the CLI")
	}
	bin := filepath.Join(t.TempDir(), "stipulator")
	if out, err := exec.Command("go", "build", "-o", bin, "github.com/greatliontech/stipulator/cmd/stipulator").CombinedOutput(); err != nil {
		t.Fatalf("building the CLI: %v\n%s", err, out)
	}
	dir := t.TempDir()
	write := func(path, content string) {
		t.Helper()
		full := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	read := func(path string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(path)))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	stale := strings.Repeat("0", 64)
	write(".stipulator/manifest.textproto", "include: \"specs/**/*.md\"\n")
	write("specs/s.md", "# S\n\n**REQ-pc-a** (behavior): It MUST a.\n\n**REQ-pc-b** (behavior): It MUST b.\n")
	write(".stipulator/bindings/b.textproto",
		"bindings {\n  requirement_id: \"REQ-pc-a\"\n  content_hash: \""+stale+"\"\n  backend: \"go\"\n  symbol: \"example.com/p.A\"\n  role: BINDING_ROLE_IMPLEMENTS\n}\n"+
			"bindings {\n  requirement_id: \"REQ-pc-b\"\n  backend: \"go\"\n  symbol: \"example.com/p.B\"\n  role: BINDING_ROLE_IMPLEMENTS\n}\n")

	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command(bin, append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "NO_COLOR=1")
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		if err := cmd.Run(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out.String())
		}
		return out.String()
	}

	out := run("pin")
	if !strings.Contains(out, "awaiting re-consent (pin --req): REQ-pc-a") {
		t.Fatalf("blanket pin conceals the preserved differing pin:\n%s", out)
	}
	after := read(".stipulator/bindings/b.textproto")
	if !strings.Contains(after, stale) {
		t.Fatalf("blanket pin laundered the differing content pin:\n%s", after)
	}
	_, tail, found := strings.Cut(after, "REQ-pc-b")
	if !found || !strings.Contains(tail, "content_hash") {
		t.Fatalf("unset pin not backfilled:\n%s", after)
	}

	// A repeat blanket run writes nothing, yet the differing pin still
	// awaits: the no-op wording must not claim quiescence.
	out = run("pin")
	if strings.Contains(out, "all pins current") {
		t.Fatalf("no-op beside a preserved differing pin claims quiescence:\n%s", out)
	}
	if !strings.Contains(out, "no pins backfilled") || !strings.Contains(out, "awaiting re-consent (pin --req): REQ-pc-a") {
		t.Fatalf("no-op beside a preserved differing pin misreported:\n%s", out)
	}

	run("pin", "--req", "REQ-pc-a")
	if strings.Contains(read(".stipulator/bindings/b.textproto"), stale) {
		t.Fatal("--req re-consent did not rewrite the differing pin")
	}
	out = run("pin")
	if !strings.Contains(out, "all pins current") || strings.Contains(out, "awaiting re-consent") {
		t.Fatalf("quiescent blanket pin output wrong:\n%s", out)
	}
}

// The CLI arms of the shape-reporting contract: the ids form names a
// shape mismatch it will not fix instead of claiming quiescence, and
// the blanket form names the shape pins it rewrote — over a real
// module so resolution produces real shapes (REQ-pin-backfill).
//
// Deliberately not //gofresh:pure: builds and executes the CLI binary.
func TestPinCLIReportsMovedShapes(t *testing.T) {
	stipulate.Covers(t, "REQ-pin-backfill")
	if testing.Short() {
		t.Skip("builds the CLI")
	}
	bin := filepath.Join(t.TempDir(), "stipulator")
	if out, err := exec.Command("go", "build", "-o", bin, "github.com/greatliontech/stipulator/cmd/stipulator").CombinedOutput(); err != nil {
		t.Fatalf("building the CLI: %v\n%s", err, out)
	}
	dir := t.TempDir()
	write := func(path, content string) {
		t.Helper()
		full := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/shapes\n\ngo 1.26\n")
	write("f.go", "package shapes\n\nfunc F() int { return 1 }\n")
	write(".stipulator/manifest.textproto", "include: \"specs/**/*.md\"\n")
	write("specs/s.md", "# S\n\n**REQ-sh-a** (behavior): It MUST a.\n")
	write(".stipulator/bindings/b.textproto",
		"bindings {\n  requirement_id: \"REQ-sh-a\"\n  backend: \"go\"\n  symbol: \"example.com/shapes.F\"\n  role: BINDING_ROLE_IMPLEMENTS\n  shape_hash: \""+strings.Repeat("a", 64)+"\"\n}\n")
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command(bin, append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "NO_COLOR=1", "GOWORK=off", "GOFLAGS=")
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		if err := cmd.Run(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out.String())
		}
		return out.String()
	}

	out := run("pin", "--req", "REQ-sh-a")
	if !strings.Contains(out, "re-pinned") || !strings.Contains(out, "shape of example.com/shapes.F moved") {
		t.Fatalf("ids form concealed the shape mismatch beside the re-pin:\n%s", out)
	}
	out = run("pin", "--req", "REQ-sh-a")
	if !strings.Contains(out, "clause pins current; shape of example.com/shapes.F moved") {
		t.Fatalf("ids no-op beside a shape mismatch claims quiescence:\n%s", out)
	}
	out = run("pin")
	if !strings.Contains(out, "shape pins refreshed (bound implementation moved): example.com/shapes.F") {
		t.Fatalf("blanket pin cleared the shape-mismatch signal invisibly:\n%s", out)
	}
	out = run("pin", "--req", "REQ-sh-a")
	if !strings.Contains(out, "REQ-sh-a: pins current") || strings.Contains(out, "moved") {
		t.Fatalf("quiescent ids answer wrong:\n%s", out)
	}
}
