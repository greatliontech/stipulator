package golang

import (
	"context"
	"go/types"
	"reflect"
	"strings"
	"testing"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/gotool"
	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/stipulate"
)

// TestPolicySelectionsRetainEveryDimension compares the projected load
// inputs with the policy's declarations, including untagged selections.
func TestPolicySelectionsRetainEveryDimension(t *testing.T) {
	stipulate.Covers(t, "REQ-go-build-selections")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	var invocations []*stipulatorv1.PolicyInvocation
	add := func(name, toolchain string, mode stipulatorv1.GoModuleMode, race bool, tags ...string) {
		cfg := &stipulatorv1.GoInvocationConfig{}
		cfg.SetPackages([]string{"./..."})
		cfg.SetToolchain(toolchain)
		cfg.SetModuleMode(mode)
		cfg.SetRace(race)
		cfg.SetTags(tags)
		invocations = append(invocations, goInvocation(name, cfg))
	}
	add("a-default", "", 0, false)
	add("b-toolchain", "local", 0, false)
	add("c-mod", "", stipulatorv1.GoModuleMode_GO_MODULE_MODE_MOD, false)
	add("d-readonly", "", stipulatorv1.GoModuleMode_GO_MODULE_MODE_READONLY, false)
	add("e-vendor", "", stipulatorv1.GoModuleMode_GO_MODULE_MODE_VENDOR, false)
	add("f-tagged", "local", stipulatorv1.GoModuleMode_GO_MODULE_MODE_MOD, true, "z", "a", "race")
	add("g-duplicate", "local", stipulatorv1.GoModuleMode_GO_MODULE_MODE_MOD, false, "race", "a", "z")
	p := &stipulatorv1.TestPolicy{}
	p.SetInvocations(invocations)
	writePolicyRecord(t, dir, p)
	selections, rejected, err := policyBuildSelections(dir)
	if err != nil || len(rejected) != 0 {
		t.Fatalf("selections refused: %v, %v", rejected, err)
	}
	type input struct {
		flags     []string
		toolchain string
	}
	var got []input
	for _, sel := range selections {
		env := selectionViewEnv([]string{"GOTOOLCHAIN=ambient"}, sel)
		toolchain, _ := gotool.LookupEnv(env, "GOTOOLCHAIN")
		got = append(got, input{selectionViewFlags(sel), toolchain})
	}
	want := []input{
		{nil, "ambient"},
		{nil, "local"},
		{[]string{"-mod=mod"}, "ambient"},
		{[]string{"-mod=readonly"}, "ambient"},
		{[]string{"-mod=vendor"}, "ambient"},
		{[]string{"-tags=a,race,z", "-mod=mod"}, "local"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("load inputs = %#v; want %#v", got, want)
	}
	served, err := NewServed(t.Context(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer served.Close()
	keys := []string{"default", "\x00local\x00\x00\x00", "\x00\x00-mod=mod\x00\x00", "\x00\x00-mod=readonly\x00\x00", "\x00\x00-mod=vendor\x00\x00", "a,race,z\x00local\x00-mod=mod\x00\x00"}
	if len(served.sels) != len(keys) {
		t.Fatalf("serving form holds %d views, want %d", len(served.sels), len(keys))
	}
	for i, key := range keys {
		sel, found := served.selection(key)
		if !found || !reflect.DeepEqual(selectionViewFlags(sel), want[i].flags) || sel.toolchain != selections[i].toolchain {
			t.Errorf("serving form lost selection %q: %+v", key, sel)
		}
	}
}

// TestUntaggedSelectionEnforcesItsToolchainPin catches a view omitted
// solely because it carries no tags. The sample avoids any toolchain fetch.
func TestUntaggedSelectionEnforcesItsToolchainPin(t *testing.T) {
	if testing.Short() {
		t.Skip("loads a policy's default view before judging the declared view")
	}
	stipulate.Covers(t, "REQ-go-build-selections", "REQ-policy-toolchain-pin")
	neutralAmbient(t)
	dir := buildSelectionModule(t)
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./..."})
	cfg.SetToolchain("go1.26.5")
	p := &stipulatorv1.TestPolicy{}
	p.SetInvocations([]*stipulatorv1.PolicyInvocation{goInvocation("untagged", cfg)})
	writePolicyRecord(t, dir, p)
	local := hostGoVersion(t)
	original := toolchainSampleForTest
	t.Cleanup(func() { toolchainSampleForTest = original })
	toolchainSampleForTest = func(context.Context, string, []string) (string, error) { return local, nil }
	if _, err := newContext(t.Context(), dir, nil); err == nil || !strings.Contains(err.Error(), `pin "go1.26.5" is not satisfied`) {
		t.Fatalf("untagged toolchain pin: %v", err)
	}
}

// TestUnavailableUntaggedSelectionKeepsTheDefaultView pins the failure
// boundary: a declared toolchain's failed load does not erase healthy views.
func TestUnavailableUntaggedSelectionKeepsTheDefaultView(t *testing.T) {
	if testing.Short() {
		t.Skip("loads the default view and a deliberately unavailable selection")
	}
	stipulate.Covers(t, "REQ-go-build-selections")
	neutralAmbient(t)
	dir := buildSelectionModule(t)
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./..."})
	cfg.SetToolchain("definitely-not-a-toolchain")
	p := &stipulatorv1.TestPolicy{}
	p.SetInvocations([]*stipulatorv1.PolicyInvocation{goInvocation("untagged", cfg)})
	writePolicyRecord(t, dir, p)
	b, err := newContext(t.Context(), dir, nil)
	if err != nil {
		t.Fatalf("unavailable declared view erased the default: %v", err)
	}
	if res, _, key, err := b.ResolveIn("example.com/tagged.Plain"); err != nil || res != verify.Resolved || key != "default" {
		t.Fatalf("default resolution: %v %q %v", res, key, err)
	}
	if _, _, err := b.Resolve("example.com/tagged.Missing"); err == nil || !strings.Contains(err.Error(), "toolchain definitely-not-a-toolchain") {
		t.Fatalf("missing symbol lost the unloaded selection: %v", err)
	}
}

// TestModuleModeReachesEagerLazyAndFingerprintLoads uses two distinct
// dependency trees under the same tags: module mode decides the dependency's
// type and code. Each kind of reader must observe the selected tree.
func TestModuleModeReachesEagerLazyAndFingerprintLoads(t *testing.T) {
	if testing.Short() {
		t.Skip("loads module and vendor views and captures their source fingerprints")
	}
	stipulate.Covers(t, "REQ-go-build-selections", "REQ-evidence-resolution-cache-format")
	neutralAmbient(t)
	dir := writeModule(t, map[string]string{
		"go.mod":                        "module example.com/selection\n\ngo 1.27\nrequire example.com/dep v1.0.0\nreplace example.com/dep => ./dep\n",
		"dep/go.mod":                    "module example.com/dep\n\ngo 1.27\n",
		"dep/dep.go":                    "package dep\nvar Choice int\nfunc Value() int { return 1 }\n",
		"vendor/modules.txt":            "# example.com/dep v1.0.0 => ./dep\n## explicit; go 1.27\nexample.com/dep\n# example.com/dep => ./dep\n",
		"vendor/example.com/dep/dep.go": "package dep\nvar Choice string\nfunc Value() int { return 2 }\n",
		"p/plain.go":                    "package p\nvar Plain int\n",
		"p/selected.go":                 "//go:build selected\n\npackage p\nimport \"example.com/dep\"\nvar Picked = dep.Choice\nfunc Value() int { return dep.Value() }\n",
		"p/selected_test.go":            "//go:build selected\n\npackage p\nimport (\"testing\"; \"example.com/selection/helper\")\nfunc TestSelected(*testing.T) { helper.Use() }\n",
		"helper/helper.go":              "package helper\nimport \"example.com/dep\"\nvar Picked = dep.Choice\nfunc Use() { _ = Picked }\n",
	})
	p := &stipulatorv1.TestPolicy{}
	var invocations []*stipulatorv1.PolicyInvocation
	for _, mode := range []stipulatorv1.GoModuleMode{stipulatorv1.GoModuleMode_GO_MODULE_MODE_MOD, stipulatorv1.GoModuleMode_GO_MODULE_MODE_VENDOR} {
		cfg := &stipulatorv1.GoInvocationConfig{}
		cfg.SetPackages([]string{"./p"})
		cfg.SetTags([]string{"selected"})
		cfg.SetModuleMode(mode)
		invocations = append(invocations, goInvocation(mode.String(), cfg))
	}
	p.SetInvocations(invocations)
	writePolicyRecord(t, dir, p)
	b, err := newContext(t.Context(), dir, []string{"example.com/selection/p"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"selected\x00\x00-mod=mod\x00\x00": "int", "selected\x00\x00-mod=vendor\x00\x00": "string"}
	seen := map[string]bool{}
	for _, pkg := range b.pkgs {
		key := b.selectionOf(pkg)
		typ, relevant := want[key]
		if !relevant || seen[key] || pkg.PkgPath != "example.com/selection/p" {
			continue
		}
		// The test variant holds the helper reference. The plain variant
		// is also a root, but has no such call to drive an on-demand load.
		var fn *types.Func
		for _, obj := range pkg.TypesInfo.Uses {
			if f, ok := obj.(*types.Func); ok && f.Pkg() != nil && f.Pkg().Path() == "example.com/selection/helper" && f.Name() == "Use" {
				fn = f
				break
			}
		}
		if fn == nil {
			continue
		}
		if len(pkg.Errors) != 0 {
			t.Fatalf("%q: %v", key, pkg.Errors)
		}
		if obj := pkg.Types.Scope().Lookup("Picked"); obj == nil || obj.Type().String() != typ {
			t.Fatalf("%q eager dependency type: %v, want %s", key, obj, typ)
		}
		if b.packageLoaded(key, "example.com/selection/helper") {
			t.Fatalf("%q helper already loaded: fixture did not exercise the lazy path", key)
		}
		b.walkMu.Lock()
		fd, helper, err := b.funcDeclOf(key, fn)
		b.walkMu.Unlock()
		if err != nil || fd == nil || helper == nil {
			t.Fatalf("%q lazy helper: %v", key, err)
		}
		if obj := helper.Types.Scope().Lookup("Picked"); obj == nil || obj.Type().String() != typ {
			t.Fatalf("%q lazy dependency type: %v, want %s", key, obj, typ)
		}
		seen[key] = true
	}
	if len(seen) != len(want) {
		t.Fatalf("typed selections reached %v, want both module and vendor views", seen)
	}
	sels, _, err := policyBuildSelections(dir)
	if err != nil {
		t.Fatal(err)
	}
	var fps []gofresh.Fingerprint
	subject := gofresh.Subject{Package: "example.com/selection/p", Symbol: "Value"}
	for _, sel := range sels {
		if sel.isDefault() {
			continue
		}
		engine, err := selectionEngine(t.Context(), selectedContextForTest(t, dir, sel), newToolchainSample())
		if err != nil {
			t.Fatal(err)
		}
		view, err := engine.NewView(t.Context(), []gofresh.Subject{subject}, dir)
		if err != nil {
			t.Fatal(err)
		}
		captured, err := view.CaptureBatch(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		fps = append(fps, captured[subject])
	}
	if len(fps) != 2 || fps[0].MaximalClosure == "" || fps[1].MaximalClosure == "" || fps[0].MaximalClosure == fps[1].MaximalClosure || fps[0].Guards.BuildConfig == fps[1].Guards.BuildConfig {
		t.Fatalf("module-mode captures did not distinguish the producing sources/configuration: %+v", fps)
	}
}

// TestSelectionKeyAndLabelDescribeTheCompleteView anchors the persisted
// key and its human reading separately, including tagless declarations.
func TestSelectionKeyAndLabelDescribeTheCompleteView(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-resolution-cache-format", "REQ-go-build-selections")
	for _, row := range []struct {
		sel        buildSelection
		key, label string
	}{
		{buildSelection{}, "default", "default"},
		{buildSelection{toolchain: "local"}, "\x00local\x00\x00\x00", "toolchain local"},
		{buildSelection{moduleMode: stipulatorv1.GoModuleMode_GO_MODULE_MODE_READONLY}, "\x00\x00-mod=readonly\x00\x00", "-mod=readonly"},
		{buildSelection{tags: []string{"a", "race"}, toolchain: "local", moduleMode: stipulatorv1.GoModuleMode_GO_MODULE_MODE_MOD}, "a,race\x00local\x00-mod=mod\x00\x00", "-tags=a,race toolchain local -mod=mod"},
		{buildSelection{moduleRoot: "consumer", workspaceMode: stipulatorv1.GoWorkspaceMode_GO_WORKSPACE_MODE_OFF}, "\x00\x00\x00consumer\x00off", "module-root consumer workspace off"},
	} {
		if got := row.sel.key(); got != row.key {
			t.Errorf("key = %q, want %q", got, row.key)
		}
		if got := viewLabel(row.key); got != row.label {
			t.Errorf("label = %q, want %q", got, row.label)
		}
	}
}
