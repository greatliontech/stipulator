package golang

import (
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/greatliontech/gofresh"
	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/stipulate"
)

func TestResolutionWorkspaceModeKeepsItsDependencyGraph(t *testing.T) {
	if testing.Short() {
		t.Skip("loads workspace and independent graphs under the same tags")
	}
	stipulate.Covers(t, "REQ-go-build-selections", "REQ-go-workspace")
	neutralAmbient(t)
	dir := writeModule(t, map[string]string{
		"go.work":          "go 1.27\nuse (\n ./main\n ./workspace\n)\n",
		"main/go.mod":      "module example.com/main\n\ngo 1.27\nrequire example.net/lib v0.0.0\nreplace example.net/lib => ../fallback\n",
		"main/plain.go":    "package mainpkg\nfunc Plain() {}\n",
		"main/chosen.go":   "//go:build chosen\n\npackage mainpkg\nimport \"example.net/lib\"\nvar Choice = lib.Marker\nfunc Read() int { return lib.Value() }\n",
		"workspace/go.mod": "module example.net/lib\n\ngo 1.27\n",
		"workspace/lib.go": "package lib\nvar Marker int\nfunc Value() int { return 1 }\n",
		"fallback/go.mod":  "module example.net/lib\n\ngo 1.27\n",
		"fallback/lib.go":  "package lib\nvar Marker string\nfunc Value() int { return 2 }\n",
	})
	p := &stipulatorv1.TestPolicy{}
	var invs []*stipulatorv1.PolicyInvocation
	for _, mode := range []stipulatorv1.GoWorkspaceMode{stipulatorv1.GoWorkspaceMode_GO_WORKSPACE_MODE_OFF, stipulatorv1.GoWorkspaceMode_GO_WORKSPACE_MODE_WORKSPACE} {
		cfg := &stipulatorv1.GoInvocationConfig{}
		cfg.SetPackages([]string{"./..."})
		cfg.SetModuleRoot("main")
		cfg.SetTags([]string{"chosen"})
		cfg.SetWorkspaceMode(mode)
		invs = append(invs, goInvocation(mode.String(), cfg))
	}
	p.SetInvocations(invs)
	writePolicyRecord(t, dir, p)
	b, err := newContext(t.Context(), dir, []string{"example.com/main"})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, pkg := range b.pkgs {
		if pkg.PkgPath != "example.com/main" {
			continue
		}
		obj := pkg.Types.Scope().Lookup("Choice")
		if obj == nil {
			continue
		}
		key := b.selectionOf(pkg)
		want := "int"
		if strings.HasSuffix(key, "\x00off") {
			want = "string"
		}
		if obj.Type().String() != want {
			t.Errorf("view %q type=%s, want %s", key, obj.Type(), want)
		}
		seen[want] = true
	}
	if len(seen) != 2 {
		t.Fatalf("did not load both dependency graphs: %v", seen)
	}
	sels, _, err := policyBuildSelections(dir)
	if err != nil {
		t.Fatal(err)
	}
	var captures []gofresh.Fingerprint
	for _, sel := range sels {
		if len(sel.tags) == 0 {
			continue
		}
		engine, err := selectionEngine(t.Context(), selectedContextForTest(t, dir, sel), newToolchainSample())
		if err != nil {
			t.Fatal(err)
		}
		subject := gofresh.Subject{Package: "example.com/main", Symbol: "Read"}
		view, err := engine.Snapshot(t.Context(), []gofresh.Subject{subject}, gofresh.SourceResult)
		if err != nil {
			t.Fatal(err)
		}
		fp, err := view.Capture(t.Context(), subject)
		if err != nil {
			t.Fatal(err)
		}
		captures = append(captures, fp)
	}
	// Workspace selection changes the dependency graph. The native closure,
	// rather than a prescribed guard field, must retain that distinction.
	if len(captures) != 2 || captures[0].MaximalClosure == captures[1].MaximalClosure {
		t.Fatalf("workspace context lost in fingerprints: %+v", captures)
	}
	if res, _, err := b.Resolve("example.com/main.Choice"); err != nil || res != verify.Resolved {
		t.Fatalf("declared choice: %v %v", res, err)
	}
}

func TestResolutionRefusesInvalidDeclaredModuleRoots(t *testing.T) {
	stipulate.Covers(t, "REQ-go-workspace", "REQ-go-build-selections")
	for _, root := range []string{"../outside", "absent"} {
		t.Run(root, func(t *testing.T) {
			dir := writeModule(t, map[string]string{"go.mod": "module example.com/invalid\n\ngo 1.27\n"})
			cfg := &stipulatorv1.GoInvocationConfig{}
			cfg.SetPackages([]string{"./..."})
			cfg.SetModuleRoot(root)
			p := &stipulatorv1.TestPolicy{}
			p.SetInvocations([]*stipulatorv1.PolicyInvocation{goInvocation("bad", cfg)})
			writePolicyRecord(t, dir, p)
			if _, _, err := policyBuildSelections(dir); err == nil || !strings.Contains(err.Error(), "module_root") {
				t.Fatalf("invalid module root %q: %v", root, err)
			}
		})
	}
}

// Every accepted context's default view precedes non-default selections,
// even when policy order names an earlier module's tagged declaration first.
func TestIndependentDefaultsPrecedeDeclaredSelections(t *testing.T) {
	if testing.Short() {
		t.Skip("loads independent roots sharing a package identity")
	}
	stipulate.Covers(t, "REQ-go-build-selections")
	neutralAmbient(t)
	dir := writeModule(t, map[string]string{
		"go.mod":    "module example.com/outer\n\ngo 1.27\n",
		"p.go":      "package outer\n",
		"a/go.mod":  "module example.net/shared\n\ngo 1.27\n",
		"a/base.go": "package shared\nfunc Anchor() {}\n",
		"a/tag.go":  "//go:build chosen\n\npackage shared\nfunc F(int) {}\n",
		"b/go.mod":  "module example.net/shared\n\ngo 1.27\n",
		"b/base.go": "package shared\nfunc F(string) {}\n",
	})
	p := &stipulatorv1.TestPolicy{}
	var invs []*stipulatorv1.PolicyInvocation
	for _, root := range []string{"a", "b"} {
		cfg := &stipulatorv1.GoInvocationConfig{}
		cfg.SetModuleRoot(root)
		cfg.SetWorkspaceMode(stipulatorv1.GoWorkspaceMode_GO_WORKSPACE_MODE_OFF)
		cfg.SetTags([]string{"chosen"})
		cfg.SetPackages([]string{"./..."})
		invs = append(invs, goInvocation(root, cfg))
	}
	p.SetInvocations(invs)
	writePolicyRecord(t, dir, p)
	b, err := newContext(t.Context(), dir, []string{"example.net/shared"})
	if err != nil {
		t.Fatal(err)
	}
	if file, ok := b.SymbolFile("example.net/shared.F"); !ok || file != "b/base.go" {
		t.Fatalf("first declaring default file = %q, %t", file, ok)
	}
	if _, _, key, err := b.ResolveIn("example.net/shared.F"); err != nil || key != "\x00\x00\x00b\x00off" {
		t.Fatalf("default selection = %q, %v", key, err)
	}
	decls, err := NewDeclarations(t.Context(), dir, []string{"example.net/shared.F"})
	if err != nil {
		t.Fatal(err)
	}
	defer decls.Close()
	_, want, _, _ := b.ResolveIn("example.net/shared.F")
	if res, shape, key, err := decls.ResolveIn("example.net/shared.F"); err != nil || res != verify.Resolved || shape != want || key != "\x00\x00\x00b\x00off" {
		t.Fatalf("named declaration = %v %q %q %v, want independent default shape %q", res, shape, key, err, want)
	}
}

func TestCorpusRootWorkspaceOffHasItsDefaultView(t *testing.T) {
	if testing.Short() {
		t.Skip("loads an independent root outside its declared workspace")
	}
	stipulate.Covers(t, "REQ-go-build-selections", "REQ-go-workspace")
	neutralAmbient(t)
	dir := writeModule(t, map[string]string{
		"go.mod":   "module example.net/shared\n\ngo 1.27\n",
		"go.work":  "go 1.27\nuse ./b\n",
		"base.go":  "//go:build !chosen\n\npackage shared\nfunc F(string) {}\n",
		"tag.go":   "//go:build chosen\n\npackage shared\nfunc F(int) {}\n",
		"b/go.mod": "module example.net/other\n\ngo 1.27\n",
		"b/b.go":   "package other\n",
	})
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./..."})
	cfg.SetTags([]string{"chosen"})
	cfg.SetWorkspaceMode(stipulatorv1.GoWorkspaceMode_GO_WORKSPACE_MODE_OFF)
	p := &stipulatorv1.TestPolicy{}
	p.SetInvocations([]*stipulatorv1.PolicyInvocation{goInvocation("root", cfg)})
	writePolicyRecord(t, dir, p)
	b, err := newContext(t.Context(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if file, ok := b.SymbolFile("example.net/shared.F"); !ok || file != "base.go" {
		t.Fatalf("root-OFF default file=%q, %t", file, ok)
	}
}

func TestWorkspaceMemberSelectionsDeduplicateAndFilterSecondaryOpeningCaptures(t *testing.T) {
	stipulate.Covers(t, "REQ-go-build-selections", "REQ-go-workspace", "REQ-evidence-resolution-freshness-typed-load")
	if testing.Short() {
		t.Skip("loads multi-member workspace and independent module resolution views")
	}
	neutralAmbient(t)
	t.Setenv("HOME", t.TempDir())
	dir := writeModule(t, map[string]string{
		"go.work":         "go 1.27\nuse (\n .\n ./sub1\n ./sub2\n)\n",
		"go.mod":          "module example.com/root\n\ngo 1.27\n",
		"root.go":         "package root\ntype RootConfig struct{ Limit int }\nconst RootDefault = 10\nfunc RootFn() int { return RootDefault }\n",
		"sub1/go.mod":     "module example.com/root/sub1\n\ngo 1.27\n",
		"sub1/s1.go":      "package sub1\nfunc Sub1Fn() int { return 2 }\n",
		"sub1/s1_race.go": "//go:build race\n\npackage sub1\ntype RaceOnlyType int\nfunc RaceOnlyFn() int { return 99 }\n",
		"sub2/go.mod":     "module example.com/root/sub2\n\ngo 1.27\n",
		"sub2/s2.go":      "package sub2\nfunc Sub2Fn() int { return 3 }\n",
		"indep/go.mod":    "module example.net/indep\n\ngo 1.27\n",
		"indep/indep.go":  "package indep\nfunc IndepFn() int { return 4 }\n",
	})

	var invs []*stipulatorv1.PolicyInvocation
	for _, m := range []string{"", "sub1", "sub2"} {
		cfg := &stipulatorv1.GoInvocationConfig{}
		cfg.SetModuleRoot(m)
		cfg.SetPackages([]string{"./..."})
		cfg.SetRace(true)
		invs = append(invs, goInvocation("ws-"+m, cfg))
	}
	indepCfg := &stipulatorv1.GoInvocationConfig{}
	indepCfg.SetModuleRoot("indep")
	indepCfg.SetWorkspaceMode(stipulatorv1.GoWorkspaceMode_GO_WORKSPACE_MODE_OFF)
	indepCfg.SetPackages([]string{"./..."})
	indepCfg.SetRace(true)
	invs = append(invs, goInvocation("z-indep", indepCfg))

	p := &stipulatorv1.TestPolicy{}
	p.SetInvocations(invs)
	writePolicyRecord(t, dir, p)

	sels, _, err := policyBuildSelections(dir)
	if err != nil {
		t.Fatal(err)
	}
	var gotKeys []string
	for _, sel := range sels {
		gotKeys = append(gotKeys, sel.key())
	}
	wantKeys := []string{
		"default",
		"\x00\x00\x00indep\x00off",
		"race\x00\x00\x00\x00",
		"race\x00\x00\x00indep\x00off",
	}
	if !slices.Equal(gotKeys, wantKeys) {
		t.Fatalf("policyBuildSelections = %q, want %q", gotKeys, wantKeys)
	}

	// Untagged workspace callables and non-callables resolve in "default" and
	// capture multi-view SourceResult entries across workspace selections while
	// independent module views ("indep") skip gofresh subject loads completely.
	untaggedSyms := []string{
		"example.com/root.RootConfig",
		"example.com/root.RootConfig.Limit",
		"example.com/root.RootDefault",
		"example.com/root.RootFn",
		"example.com/root/sub1.Sub1Fn",
		"example.com/root/sub2.Sub2Fn",
	}
	s, err := NewServed(t.Context(), dir, untaggedSyms)
	if err != nil {
		t.Fatal(err)
	}
	var indepEngineLoads atomic.Int32
	func() {
		oldEngineHook := engineCommandHook
		defer func() { engineCommandHook = oldEngineHook }()
		engineCommandHook = func(cmd *exec.Cmd) {
			if strings.HasSuffix(filepath.ToSlash(cmd.Dir), "/indep") && slices.Contains(cmd.Args, "list") {
				indepEngineLoads.Add(1)
			}
		}
		for _, sym := range untaggedSyms {
			res, _, selKey, err := s.ResolveIn(sym)
			if err != nil || res != verify.Resolved || selKey != "default" {
				t.Fatalf("ResolveIn(%s) = (%v, %q, %v), want Resolved under default", sym, res, selKey, err)
			}
		}
	}()
	if got := indepEngineLoads.Load(); got != 0 ||
		len(s.openingViews["default"].present) != len(untaggedSyms) ||
		len(s.openingViews["race\x00\x00\x00\x00"].present) != len(untaggedSyms) ||
		len(s.openingViews["\x00\x00\x00indep\x00off"].present) != 0 ||
		len(s.openingViews["\x00\x00\x00indep\x00off"].absent) != len(untaggedSyms) {
		t.Fatalf("openingCapture indepEngineLoads=%d default=%d race=%d indep=(%d,%d), want 0/%d/%d/(0,%d)",
			got,
			len(s.openingViews["default"].present),
			len(s.openingViews["race\x00\x00\x00\x00"].present),
			len(s.openingViews["\x00\x00\x00indep\x00off"].present),
			len(s.openingViews["\x00\x00\x00indep\x00off"].absent),
			len(untaggedSyms), len(untaggedSyms), len(untaggedSyms))
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// Race-tagged callable and non-callable symbols not present in "default" remain in
	// remaining, capture under "race\x00\x00\x00\x00", publish, and serve warm alongside
	// an independent-module symbol.
	mixedSyms := append(slices.Clone(untaggedSyms),
		"example.com/root/sub1.RaceOnlyType",
		"example.com/root/sub1.RaceOnlyFn",
		"example.net/indep.IndepFn",
	)
	mixed, err := NewServed(t.Context(), dir, mixedSyms)
	if err != nil {
		t.Fatal(err)
	}
	if mixed.ServedCount() != len(untaggedSyms) {
		t.Fatalf("mixed initial ServedCount = %d, want %d", mixed.ServedCount(), len(untaggedSyms))
	}
	for _, sym := range mixedSyms {
		if res, _, err := mixed.Resolve(sym); err != nil || res != verify.Resolved {
			t.Fatalf("mixed Resolve(%s) = (%v, %v), want Resolved", sym, res, err)
		}
	}
	if err := mixed.Close(); err != nil {
		t.Fatal(err)
	}

	warmMixed, err := NewServed(t.Context(), dir, mixedSyms)
	if err != nil {
		t.Fatal(err)
	}
	defer warmMixed.Close()
	if warmMixed.ServedCount() != len(mixedSyms) {
		t.Fatalf("warmMixed ServedCount = %d, want %d (%v)", warmMixed.ServedCount(), len(mixedSyms), warmMixed.Notices())
	}

	// Non-ErrNotExist stat errors and missing required go.work propagate through goworkEnv / goworkModeEnv.
	if _, err := goworkEnv(filepath.Join(dir, "go.mod", "not-a-dir")); err == nil {
		t.Fatal("goworkEnv on ENOTDIR path succeeded, want stat error")
	}
	if _, err := goworkModeEnv(t.TempDir(), stipulatorv1.GoWorkspaceMode_GO_WORKSPACE_MODE_WORKSPACE); err == nil {
		t.Fatal("goworkModeEnv WORKSPACE without go.work succeeded, want error")
	}
}
