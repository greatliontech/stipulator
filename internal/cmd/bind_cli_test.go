package cmd

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/author"
	"github.com/greatliontech/stipulator/stipulate"
)

// TestBindClaimsAlignment pins the repeated-flag alignment: one claim
// per --req with exactly one --symbol each; --role, --backend, and
// --file once (applying to every claim) or exactly once per claim; the
// backend defaulting to go; and every other count refused with both
// counts named — never aligned by guess, never dropped.
//
//gofresh:pure
func TestBindClaimsAlignment(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-claim-batch")
	claims, err := bindClaims(
		[]string{"REQ-a", "REQ-b"},
		[]string{"example.com/p.TestA", "example.com/p.TestB"},
		[]string{"tests"},
		nil,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 2 {
		t.Fatalf("claims = %d, want one per --req", len(claims))
	}
	for i, want := range []author.BindRequest{
		{Requirement: "REQ-a", Symbol: "example.com/p.TestA", Backend: "go", Role: stipulatorv1.BindingRole_BINDING_ROLE_TESTS},
		{Requirement: "REQ-b", Symbol: "example.com/p.TestB", Backend: "go", Role: stipulatorv1.BindingRole_BINDING_ROLE_TESTS},
	} {
		if claims[i] != want {
			t.Errorf("claim %d = %+v, want %+v", i, claims[i], want)
		}
	}
	perClaim, err := bindClaims(
		[]string{"REQ-a", "REQ-b"},
		[]string{"x.A", "x.B"},
		[]string{"tests", "implements"},
		[]string{"go", "go"},
		[]string{"", "custom.textproto"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if perClaim[1].Role != stipulatorv1.BindingRole_BINDING_ROLE_IMPLEMENTS || perClaim[1].File != "custom.textproto" {
		t.Errorf("per-claim values misassigned: %+v", perClaim[1])
	}
	for _, c := range []struct {
		reqs, symbols, roles []string
		wantErr              []string
	}{
		{[]string{"REQ-a", "REQ-b"}, []string{"x.A"}, []string{"tests"}, []string{"2 --req", "1 --symbol"}},
		{[]string{"REQ-a", "REQ-b"}, []string{"x.A", "x.B"}, []string{"tests", "tests", "tests"}, []string{"3 --role", "2 claim"}},
		{nil, nil, nil, []string{"at least one --req"}},
		{[]string{"REQ-a", "REQ-b"}, []string{"x.A", "x.B"}, []string{"tests", "bogus"}, []string{"claim 2 (REQ-b)"}},
	} {
		_, err := bindClaims(c.reqs, c.symbols, c.roles, nil, nil)
		if err == nil {
			t.Fatalf("mismatch %v/%v/%v accepted", c.reqs, c.symbols, c.roles)
		}
		for _, want := range c.wantErr {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("refusal %q does not name %q", err.Error(), want)
			}
		}
	}
}

// TestOneFlagRefusesRepetition pins the refuse arm at the unit tier
// (the CLI arm exercises it through a subprocess no in-process oracle
// can see): a repeated narrowing flag refuses by name, never
// last-wins.
//
//gofresh:pure
func TestOneFlagRefusesRepetition(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-claim-batch")
	if v, err := oneFlag("req", nil); err != nil || v != "" {
		t.Errorf("absent flag = %q, %v", v, err)
	}
	if v, err := oneFlag("req", []string{"REQ-x"}); err != nil || v != "REQ-x" {
		t.Errorf("single flag = %q, %v", v, err)
	}
	if _, err := oneFlag("req", []string{"REQ-a", "REQ-b"}); err == nil || !strings.Contains(err.Error(), "forms no batch") {
		t.Errorf("repetition = %v, want a named refusal", err)
	}
}

// TestSplitListsJoinsEveryOccurrence pins the batch arm for flags that
// already express multiplicity: every occurrence's comma-separated
// identifiers join — an occurrence silently dropped would be the
// accept-and-drop REQ-evidence-claim-batch forbids.
//
//gofresh:pure
func TestSplitListsJoinsEveryOccurrence(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-claim-batch")
	got := splitLists([]string{"REQ-a, REQ-b", "REQ-c", ""})
	want := []string{"REQ-a", "REQ-b", "REQ-c"}
	if len(got) != len(want) {
		t.Fatalf("splitLists = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("splitLists = %v, want %v", got, want)
		}
	}
}

// TestBindCLIBatchAllOrNothing pins the CLI batch end to end: repeated
// flag groups author every expressed claim through the all-or-nothing
// batch — the exact invocation shape that previously exited 0 while
// silently keeping only the last claim now writes both — a failing
// claim anywhere authors nothing, and unbind refuses repeated
// narrowing flags instead of last-wins.
//
// Deliberately not //gofresh:pure: builds and executes the CLI binary.
// The build shells `go build <import path>`, which compiles pristine
// source regardless of any mutation overlay, so this test decides no
// mutation probe — the unit-tier tests exist for exactly that.
func TestBindCLIBatchAllOrNothing(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-claim-batch")
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
	write(".stipulator/manifest.textproto", "include: \"specs/**/*.md\"\n")
	write("specs/s.md", "# S\n\n**REQ-bb-a** (behavior): It MUST a.\n\n**REQ-bb-b** (behavior): It MUST b.\n\n**REQ-bb-c** (behavior): It SHOULD c.\n")
	write("go.mod", "module example.com/bindbatch\n\ngo 1.26\n")
	write("p/p_test.go", "package p\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {}\n\nfunc TestB(t *testing.T) {}\n")
	run := func(wantExit int, args ...string) string {
		t.Helper()
		cmd := exec.Command(bin, append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "NO_COLOR=1")
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		err := cmd.Run()
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else if err != nil {
			t.Fatalf("running %v: %v\n%s", args, err, out.String())
		}
		if code != wantExit {
			t.Fatalf("%v exit = %d, want %d\n%s", args, code, wantExit, out.String())
		}
		return out.String()
	}

	run(0, "bind",
		"--req", "REQ-bb-a", "--symbol", "example.com/bindbatch/p.TestA",
		"--req", "REQ-bb-b", "--symbol", "example.com/bindbatch/p.TestB",
		"--role", "tests")
	recorded, err := os.ReadFile(filepath.Join(dir, ".stipulator", "bindings", "bb.textproto"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"REQ-bb-a", "TestA", "REQ-bb-b", "TestB"} {
		if !strings.Contains(string(recorded), want) {
			t.Errorf("batch dropped a claim: %q absent from the record:\n%s", want, recorded)
		}
	}
	// Pairing, not just presence: each claim's requirement rides its
	// own symbol — a swap would satisfy the substring checks above.
	for req, sym := range map[string]string{"REQ-bb-a": "TestA", "REQ-bb-b": "TestB"} {
		re := regexp.MustCompile(`requirement_id: "` + req + `"\n[^}]*` + sym + `"`)
		if !re.Match(recorded) {
			t.Errorf("claim pairing lost: %s not recorded with %s:\n%s", req, sym, recorded)
		}
	}

	out := run(2, "bind",
		"--req", "REQ-bb-a", "--symbol", "example.com/bindbatch/p.TestB",
		"--req", "REQ-nope", "--symbol", "example.com/bindbatch/p.TestB",
		"--role", "tests", "--file", ".stipulator/bindings/allornothing.textproto")
	if !strings.Contains(out, "REQ-nope") {
		t.Errorf("all-or-nothing refusal does not name the failing claim: %s", out)
	}
	if _, err := os.Stat(filepath.Join(dir, ".stipulator", "bindings", "allornothing.textproto")); !os.IsNotExist(err) {
		t.Error("a failing batch authored records")
	}

	out = run(2, "bind", "--req", "REQ-bb-a", "--req", "REQ-bb-b", "--symbol", "example.com/bindbatch/p.TestA", "--role", "tests")
	if !strings.Contains(out, "2 --req") || !strings.Contains(out, "1 --symbol") {
		t.Errorf("count-mismatch refusal does not name both counts: %s", out)
	}

	out = run(2, "unbind", "--req", "REQ-bb-a", "--req", "REQ-bb-b")
	if !strings.Contains(out, "forms no batch") {
		t.Errorf("unbind repetition not refused by name: %s", out)
	}

	// Attest writes succeed through the one applier — the CAS routing
	// itself is pinned structurally, not by this arm: the bare
	// file-write helper is deleted, so a revert to it cannot compile.
	out = run(0, "attest", "requirement", "--req", "REQ-bb-c", "--reason", "judged fine for the fixture")
	if !strings.Contains(out, "wrote .stipulator/attestations/bb-c.textproto") {
		t.Errorf("attest write did not land: %s", out)
	}

	// Every sibling claim-writing verb refuses repeated once-only
	// flags the same way — no surface keeps last-wins.
	for _, args := range [][]string{
		{"dispose", "retire", "--id", "REQ-bb-a", "--id", "REQ-bb-b", "--force"},
		{"dispose", "editorial", "--req", "REQ-bb-a", "--req", "REQ-bb-b"},
		{"gap", "--req", "REQ-bb-a", "--reason", "r1", "--reason", "r2", "--covered", "self"},
		{"attest", "requirement", "--req", "REQ-bb-a", "--req", "REQ-bb-b", "--reason", "r"},
		{"retarget", "--from", "a", "--from", "b", "--to", "c"},
	} {
		if out := run(2, args...); !strings.Contains(out, "forms no batch") {
			t.Errorf("%v repetition not refused by name: %s", args, out)
		}
	}
}
