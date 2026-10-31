package golang

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/greatliontech/gofresh/closure"
	"github.com/greatliontech/gofresh/gotool"
	"github.com/greatliontech/stipulator/stipulate"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
)

// A policy invocation whose build selection selects a standard-library
// surface violating structural audit contracts (badstd selects strings'
// unapproved linkname file) runs under a selection the freshness engine's
// toolchain-source audit refuses: observation admissions strip and serving
// degrades to execution. The policy tier names that cost where the
// tags were declared — the notice carries the invocation name and the
// engine's own rendering — while admitted selections (a tag that
// constrains no standard-library file or selects an admitted tag-gated
// standard-library variant such as netgo, or the race selection of a
// structurally verified toolchain) and non-witness invocations (which
// build no engine) raise nothing (REQ-check-policy-notices). The
// selection resolves under the invocation's own environment: a declared
// GOOS pin naming another platform raises the notice with no tag at all.
//
// Deliberately not //gofresh:pure: normalization shells the go toolchain.
func TestSelectionNoticesAttributeUnwalkedSelections(t *testing.T) {
	stipulate.Covers(t, "REQ-check-policy-notices")
	neutralAmbient(t)
	dir := discoverFixture(t)
	badRoot := unauditedTagGOROOT(t)
	t.Setenv("GOROOT", badRoot)
	t.Setenv("GOTOOLCHAIN", "local")
	ambient, err := gotool.NormalizeEnv(os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	// The arms below are race selections: the gate asks the same, so a
	// host whose race selection cannot be listed (no cgo) skips rather
	// than reads its own "could not be read" notices as the audit's.
	if notice, err := closure.ToolchainSelectionNoticeResolved(context.Background(), gotool.NewEnvReader(ownedRunner, dir, ambient), selectionBuildFlags(true, nil)); err != nil || notice != "" {
		t.Skipf("the running toolchain's race selection is not admitted here (%v, %q); the version canary covers this", err, notice)
	}
	tagged := &stipulatorv1.GoInvocationConfig{}
	tagged.SetPackages([]string{"./..."})
	tagged.SetRace(true)
	tagged.SetTags([]string{"badstd"})
	vanilla := &stipulatorv1.GoInvocationConfig{}
	vanilla.SetPackages([]string{"./..."})
	vanilla.SetRace(true)
	plainTagged := &stipulatorv1.GoInvocationConfig{}
	plainTagged.SetPackages([]string{"./..."})
	plainTagged.SetTags([]string{"badstd"})
	admittedTag := &stipulatorv1.GoInvocationConfig{}
	admittedTag.SetPackages([]string{"./..."})
	admittedTag.SetRace(true)
	admittedTag.SetTags([]string{"dup", "netgo"})
	// The selection is resolved under the invocation's OWN environment:
	// a declared GOOS pin naming another platform makes the race
	// selection unlistable (the go command refuses -race without cgo,
	// which a cross-platform build disables), which the audit refuses
	// naming the fault — with no tag at all, where the ambient
	// environment's host platform is admitted.
	platformPinned := &stipulatorv1.GoInvocationConfig{}
	platformPinned.SetPackages([]string{"./..."})
	platformPinned.SetRace(true)
	platformPinned.SetGoos("freebsd")
	pol := &stipulatorv1.TestPolicy{}
	pol.SetInvocations([]*stipulatorv1.PolicyInvocation{
		goInvocation("plain-tagged", plainTagged),
		goInvocation("tagged", tagged),
		goInvocation("vanilla", vanilla),
		goInvocation("admitted-tag", admittedTag),
		goInvocation("platform-pinned", platformPinned),
	})
	got := SelectionNotices(mustCapture(t, context.Background(), dir, pol))
	if len(got) != 2 {
		t.Fatalf("notices = %v, want exactly the two witness-eligible refused invocations'", got)
	}
	for i, arm := range [][]string{{`invocation "tagged"`, " moved in 1 keys: strings"}, {`invocation "platform-pinned"`, " could not be read: "}} {
		for _, frag := range append(arm, "toolchain-selection audit: the audited surface of ", "observation admissions are disabled") {
			if !strings.Contains(got[i], frag) {
				t.Fatalf("notice %q missing %q", got[i], frag)
			}
		}
	}
	// Notices follow record order (REQ-core-determinism): several
	// tagged invocations raise theirs in the order the record lists them.
	var ordered []*stipulatorv1.PolicyInvocation
	var wantOrder []string
	for _, name := range []string{"tagged-d", "tagged-b", "tagged-a", "tagged-c"} {
		ordered = append(ordered, goInvocation(name, tagged))
		wantOrder = append(wantOrder, fmt.Sprintf("invocation %q", name))
	}
	pol.SetInvocations(ordered)
	got = SelectionNotices(mustCapture(t, context.Background(), dir, pol))
	if len(got) != len(wantOrder) {
		t.Fatalf("notices = %v, want one per tagged invocation", got)
	}
	for i, prefix := range wantOrder {
		if !strings.HasPrefix(got[i], prefix) {
			t.Fatalf("notice %d = %q, want record order %v", i, got[i], wantOrder)
		}
	}
}

func unauditedTagGOROOT(t *testing.T) string {
	t.Helper()
	snapshot, err := (gotool.Runner{}).TakeEnvSnapshot(context.Background(), t.TempDir(), os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	origRoot := snapshot.Value("GOROOT")
	tmp, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dstRoot := filepath.Join(tmp, "goroot")
	if err := os.MkdirAll(dstRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	copyTree := func(srcDir, dstDir string, allowHardlink bool, skip func(rel string, d os.DirEntry) (skipDir, skipFile bool)) {
		if err := filepath.WalkDir(srcDir, func(path string, d os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			rel, err := filepath.Rel(srcDir, path)
			if err != nil {
				return err
			}
			if rel != "." && skip != nil {
				sd, sf := skip(rel, d)
				if sd {
					return filepath.SkipDir
				}
				if sf {
					return nil
				}
			}
			dst := filepath.Join(dstDir, rel)
			if d.IsDir() {
				return os.MkdirAll(dst, 0o755)
			}
			if d.Type()&os.ModeSymlink != 0 {
				return nil
			}
			if allowHardlink {
				if err := os.Link(path, dst); err == nil {
					return nil
				}
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			return os.WriteFile(dst, data, info.Mode().Perm())
		}); err != nil {
			t.Fatal(err)
		}
	}
	for _, topFile := range []string{"VERSION", "go.env"} {
		src := filepath.Join(origRoot, topFile)
		if data, err := os.ReadFile(src); err == nil {
			if err := os.WriteFile(filepath.Join(dstRoot, topFile), data, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	if origBin := filepath.Join(origRoot, "bin"); func() bool { _, err := os.Stat(origBin); return err == nil }() {
		dstBin := filepath.Join(dstRoot, "bin")
		if err := os.Symlink(origBin, dstBin); err != nil {
			copyTree(origBin, dstBin, true, nil)
		}
	}
	if origPkg := filepath.Join(origRoot, "pkg"); func() bool { _, err := os.Stat(origPkg); return err == nil }() {
		dstPkg := filepath.Join(dstRoot, "pkg")
		if err := os.MkdirAll(dstPkg, 0o755); err != nil {
			t.Fatal(err)
		}
		entries, err := os.ReadDir(origPkg)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			src := filepath.Join(origPkg, entry.Name())
			dst := filepath.Join(dstPkg, entry.Name())
			if entry.Name() == "include" && entry.IsDir() {
				copyTree(src, dst, false, nil)
				continue
			}
			if err := os.Symlink(src, dst); err != nil {
				copyTree(src, dst, true, nil)
			}
		}
	}
	if origLib := filepath.Join(origRoot, "lib"); func() bool { _, err := os.Stat(origLib); return err == nil }() {
		copyTree(origLib, filepath.Join(dstRoot, "lib"), true, nil)
	}
	copyTree(filepath.Join(origRoot, "src"), filepath.Join(dstRoot, "src"), true, func(rel string, d os.DirEntry) (bool, bool) {
		slash := filepath.ToSlash(rel)
		if d.IsDir() && (slash == "cmd" || d.Name() == "testdata") {
			return true, false
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), "_test.go") {
			return false, true
		}
		return false, false
	})
	badStd := "//go:build badstd\n\npackage strings\n\nimport _ \"unsafe\"\n\n//go:linkname gofreshUnapproved\nfunc gofreshUnapproved() {}\n"
	if err := os.WriteFile(filepath.Join(dstRoot, "src", "strings", "gofresh_badstd.go"), []byte(badStd), 0o644); err != nil {
		t.Fatal(err)
	}
	return dstRoot
}

// The verdict is the normalized form's: a form carrying a notice is
// reported under its name whatever environment it holds and a form
// carrying none is silent — the reader resolves nothing itself and
// judges nothing: that a non-witness invocation carries no verdict is
// the normalizer's rule, pinned by the plain-tagged arm above
// (REQ-check-policy-notices over REQ-check-derivation).
func TestSelectionNoticesReadTheFormsVerdicts(t *testing.T) {
	stipulate.Covers(t, "REQ-check-policy-notices")
	pc := &Capture{normalized: []*NormalizedInvocation{
		{Name: "noticed", Race: true, SelectionNotice: "toolchain-selection audit: synthetic", WitnessEnv: []string{"NOEQUALS"}},
		{Name: "admitted", Race: true},
	}}
	got := SelectionNotices(pc)
	if len(got) != 1 || got[0] != `invocation "noticed": toolchain-selection audit: synthetic` {
		t.Fatalf("notices = %v", got)
	}
}

// The one fault a resolution can meet past the taken snapshot is the
// run's own end: a context cancelled as the audit's listing spawns
// refuses the normalization naming the audit, as any spawn under an
// ended run would — a verdict is never stored admitted by default
// (REQ-check-policy-notices).
//
// Deliberately not //gofresh:pure: normalization shells the go toolchain.
func TestNormalizationRefusesWhenTheRunEndsMidAudit(t *testing.T) {
	stipulate.Covers(t, "REQ-check-policy-notices")
	neutralAmbient(t)
	dir := discoverFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	prior := commandHook
	commandHook = func(name string, args []string) {
		if name == "go" && len(args) > 0 && args[0] == "list" {
			cancel()
		}
	}
	t.Cleanup(func() { commandHook = prior })
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./..."})
	cfg.SetRace(true)
	// A tag of its own: a selection scope nothing under this test's
	// cache home has listed, so the audit must spawn its listing here
	// rather than serve gofresh's on-disk listing memo.
	cfg.SetTags([]string{"stipulatormidaudit"})
	_, err := NormalizeInvocation(ctx, dir, goInvocation("mid", cfg))
	if err == nil || !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "toolchain-source audit") {
		t.Fatalf("normalization under a run ended mid-audit: %v", err)
	}
}

// Normalization queries the environment exactly once: the audit's
// reader is primed with the snapshot effectiveGoEnv took, so the
// verdict costs no second `go env` — measured warm, after a
// normalization of the same selection listed its scope into gofresh's
// on-disk memo under this test's cache home, so the listing spawn is
// the memo's and the environment query stands alone
// (REQ-check-derivation).
//
// Deliberately not //gofresh:pure: normalization shells the go toolchain.
func TestNormalizationQueriesTheEnvironmentOnce(t *testing.T) {
	stipulate.Covers(t, "REQ-check-derivation")
	neutralAmbient(t)
	dir := discoverFixture(t)
	ctx := context.Background()
	c := countSpawns(t)
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./..."})
	cfg.SetRace(true)
	cfg.SetTags([]string{"stipulatoronequery"})
	inv := goInvocation("once", cfg)
	if _, err := NormalizeInvocation(ctx, dir, inv); err != nil {
		t.Fatal(err)
	}
	c.reset()
	if _, err := NormalizeInvocation(ctx, dir, inv); err != nil {
		t.Fatal(err)
	}
	if got := c.snapshot(); got.env != 1 || got.list != 0 {
		t.Fatalf("a warm normalization spawned %+v, want exactly one environment query and no listing", got)
	}
}
