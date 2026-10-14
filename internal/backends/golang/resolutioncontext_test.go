package golang

import (
	"strings"
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
		engine, err := selectionEngine(t.Context(), dir, sel, newToolchainSample())
		if err != nil {
			t.Fatal(err)
		}
		subject := gofresh.Subject{Package: "example.com/main", Symbol: "Read"}
		view, err := engine.NewView(t.Context(), []gofresh.Subject{subject}, sel.directory(dir))
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
