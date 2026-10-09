package golang_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/author"
	"github.com/greatliontech/stipulator/internal/backends/golang"
	"github.com/greatliontech/stipulator/internal/check"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/stipulate"
)

// A check spawns its resolver child exactly once: every question the
// pass has for the serving backend is asked before the witness run,
// the child is released before the first process spawns, and nothing
// the pass does after the run — correlation, coverage, the account —
// asks a question that would cost a second child
// (REQ-evidence-resolution-freshness-quiesce).
func TestCheckSpawnsTheResolverChildOnce(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-resolution-freshness-quiesce")
	if testing.Short() {
		t.Skip("executes a race-instrumented policy over a fixture tree")
	}
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("GOENV", "off")
	t.Setenv("GOFLAGS", "")
	t.Setenv("GOPACKAGESDRIVER", "")
	t.Setenv("GOTOOLCHAIN", "local")
	dir := t.TempDir()
	for path, content := range map[string]string{
		"go.mod":                         "module example.com/spawnfix\n\ngo 1.26.4\n",
		"ok/ok.go":                       "package ok\n\nfunc Double(x int) int { return 2 * x }\n",
		"ok/ok_test.go":                  "package ok\n\nimport \"testing\"\n\n//gofresh:pure\nfunc TestDouble(t *testing.T) { Double(2) }\n",
		"specs/check.md":                 "# Check\n\n**REQ-fix-must** (behavior): The fixture MUST pass.\n",
		".stipulator/manifest.textproto": "include: \"specs/**/*.md\"\n",
		".stipulator/policy.textproto":   "invocations {\n  name: \"race\"\n  timeout {\n    seconds: 300\n  }\n  go {\n    packages: \"./...\"\n    race: true\n  }\n}\n",
	} {
		full := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// The bindings are authored pinned through the declaration-reading
	// form, whose own child is not the check's: the spawn seam installs
	// after it.
	ctx := context.Background()
	gb, err := golang.NewWholeTree(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	ups, err := author.Binds(os.DirFS(dir), map[string]verify.Backend{"go": gb}, []author.BindRequest{
		{Requirement: "REQ-fix-must", Symbol: "example.com/spawnfix/ok.TestDouble", Backend: "go", Role: stipulatorv1.BindingRole_BINDING_ROLE_TESTS},
		{Requirement: "REQ-fix-must", Symbol: "example.com/spawnfix/ok.Double", Backend: "go", Role: stipulatorv1.BindingRole_BINDING_ROLE_IMPLEMENTS},
	})
	gb.Close()
	if err != nil {
		t.Fatal(err)
	}
	for _, up := range ups {
		full := filepath.Join(dir, filepath.FromSlash(up.Path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, up.Content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	children, release := golang.CountChildSpawnsForTest()
	t.Cleanup(release)
	res, err := check.Run(ctx, dir, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.GetPassed() {
		t.Fatalf("the fixture check failed:\n%s", strings.Join(res.GetResolutionNotices(), "\n"))
	}
	if got := children(); got != 1 {
		t.Fatalf("the check spawned %d resolver children; want exactly one — a second means a question was asked after the release", got)
	}
}
