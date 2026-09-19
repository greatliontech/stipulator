package golang

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"

	"google.golang.org/protobuf/types/known/durationpb"
	"pgregory.net/rapid"

	"github.com/greatliontech/gofresh/gotool"
	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/stipulate"
)

// discoverFixture is the workspace fixture the normalization and discovery
// tests share.
func discoverFixture(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("testdata", "discover"))
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

// neutralAmbient pins the ambient controls normalization reads to a known
// hermetic state, so host configuration cannot steer these tests.
func neutralAmbient(t *testing.T) {
	// An empty variable defers to the persistent go env config file; GOENV
	// off makes the values set here the only ambient source.
	t.Setenv("GOENV", "off")
	// An exported ambient workspace — a mutation-probe or CI harness
	// exporting this repo's own go.work — would leak into fixture
	// modules' go invocations and refuse their loads; the witness runner
	// pins GOWORK per module, and hermetic tests pin it off themselves.
	t.Setenv("GOWORK", "off")
	t.Helper()
	// The witness store lives under the user cache directory; tests must
	// never touch the real one.
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("GOFLAGS", "")
	t.Setenv("GOPACKAGESDRIVER", "")
	t.Setenv("GOTOOLCHAIN", "local")
	// An exported GOCACHE would out-rank the go env config file these
	// tests source values from - the witness executor itself pins one
	// into child environments, so the suite must stay hermetic when run
	// as its own witness. Empty defers to the config file.
	t.Setenv("GOCACHE", "")
}

func goInvocation(name string, cfg *stipulatorv1.GoInvocationConfig) *stipulatorv1.PolicyInvocation {
	inv := &stipulatorv1.PolicyInvocation{}
	inv.SetName(name)
	inv.SetTimeout(durationpb.New(derivedTimeout))
	inv.SetGo(cfg)
	return inv
}

// TestGoNormalizeAbsentFieldsPinEffectiveEnvironment pins the pin-at-load
// semantics: an absent field resolves to the value the tree and host
// environment select at load — concrete and visible in the normalized
// invocation — and the resolved values are pinned into the child
// environment.
func TestGoNormalizeAbsentFieldsPinEffectiveEnvironment(t *testing.T) {
	stipulate.Covers(t, "REQ-policy-explicit")
	neutralAmbient(t)
	t.Setenv("GOFLAGS", "-trimpath")
	dir := discoverFixture(t)
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./..."})
	cfg.SetRace(true)
	n, err := NormalizeInvocation(context.Background(), dir, goInvocation("race", cfg))
	if err != nil {
		t.Fatal(err)
	}
	if n.Toolchain == "" || !strings.HasPrefix(n.Toolchain, "go") && !strings.HasPrefix(n.Toolchain, "devel") {
		t.Errorf("effective toolchain not pinned: %q", n.Toolchain)
	}
	if n.GOOS == "" || n.GOARCH == "" {
		t.Errorf("effective platform not pinned: %q/%q", n.GOOS, n.GOARCH)
	}
	if n.GOFLAGS != "-trimpath" {
		t.Errorf("effective GOFLAGS = %q, want the ambient -trimpath pinned", n.GOFLAGS)
	}
	if !n.WorkspaceOn {
		t.Error("workspace mode not derived from the tree's go.work")
	}
	for key, want := range map[string]string{
		"GOOS": n.GOOS, "GOARCH": n.GOARCH, "GOFLAGS": "-trimpath",
		"GOPACKAGESDRIVER": "off",
	} {
		if got, ok := lookupEnv(n.Env, key); !ok || got != want {
			t.Errorf("child env %s = %q (present=%v), want %q pinned", key, got, ok, want)
		}
	}
	if gowork, _ := lookupEnv(n.Env, "GOWORK"); gowork != filepath.Join(dir, "go.work") {
		t.Errorf("child env GOWORK = %q, want the tree's own go.work", gowork)
	}
	if n.Timeout != derivedTimeout {
		t.Errorf("normalized timeout = %v, want the envelope's explicit %v", n.Timeout, derivedTimeout)
	}
}

// TestGoNormalizeExplicitFieldsOverrideEnvironment pins the explicit
// semantics: a present field overrides the ambient value and lands in the
// child environment.
func TestGoNormalizeExplicitFieldsOverrideEnvironment(t *testing.T) {
	stipulate.Covers(t, "REQ-policy-explicit")
	neutralAmbient(t)
	t.Setenv("GOFLAGS", "-trimpath")
	dir := discoverFixture(t)
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./..."})
	cfg.SetGoos("js")
	cfg.SetGoarch("wasm")
	cfg.SetCgoEnabled(false)
	cfg.SetGoflags("-v")
	cfg.SetTags([]string{"special"})
	cfg.SetCount(2)
	cfg.SetArgs([]string{"-quick"})
	cfg.SetWorkspaceMode(stipulatorv1.GoWorkspaceMode_GO_WORKSPACE_MODE_OFF)
	n, err := NormalizeInvocation(context.Background(), dir, goInvocation("cross", cfg))
	if err != nil {
		t.Fatal(err)
	}
	if n.GOOS != "js" || n.GOARCH != "wasm" {
		t.Errorf("explicit platform not honored: %q/%q", n.GOOS, n.GOARCH)
	}
	if n.CgoEnabled {
		t.Error("explicit cgo_enabled=false not honored")
	}
	if n.GOFLAGS != "-v" {
		t.Errorf("explicit goflags = %q, want it to replace the ambient value", n.GOFLAGS)
	}
	if n.WorkspaceOn {
		t.Error("explicit workspace_mode OFF not honored")
	}
	if gowork, _ := lookupEnv(n.Env, "GOWORK"); gowork != "off" {
		t.Errorf("child env GOWORK = %q, want off", gowork)
	}
	if got, _ := lookupEnv(n.Env, "GOOS"); got != "js" {
		t.Errorf("child env GOOS = %q, want js", got)
	}
	if len(n.Tags) != 1 || n.Tags[0] != "special" || n.Count != 2 || len(n.Args) != 1 {
		t.Errorf("explicit test inputs lost: tags=%v count=%d args=%v", n.Tags, n.Count, n.Args)
	}
}

// TestGoNormalizeEnvironmentDenialAndOverride pins the environment
// inheritance model: denial removes an inherited variable, overrides
// apply after denial, and both survive into the child environment.
func TestGoNormalizeEnvironmentDenialAndOverride(t *testing.T) {
	stipulate.Covers(t, "REQ-policy-explicit")
	neutralAmbient(t)
	t.Setenv("STIP_TEST_DENIED", "leak")
	dir := discoverFixture(t)
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./..."})
	cfg.SetEnvDeny([]string{"STIP_TEST_DENIED"})
	cfg.SetEnvironment([]string{"STIP_TEST_SET=explicit"})
	n, err := NormalizeInvocation(context.Background(), dir, goInvocation("env", cfg))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := lookupEnv(n.Env, "STIP_TEST_DENIED"); ok {
		t.Error("denied variable survived into the child environment")
	}
	if got, _ := lookupEnv(n.Env, "STIP_TEST_SET"); got != "explicit" {
		t.Errorf("environment override = %q, want explicit", got)
	}
}

// TestGoNormalizeRejectsAmbientControls pins the ambient-control refusal
// class: an effective GOFLAGS carrying an overlay or a typed-field-owned
// flag, and an ambient external package driver, refuse normalization
// rather than silently reshaping the reviewed invocation.
func TestGoNormalizeRejectsAmbientControls(t *testing.T) {
	stipulate.Covers(t, "REQ-go-owned-processes")
	dir := discoverFixture(t)
	cfg := func() *stipulatorv1.GoInvocationConfig {
		c := &stipulatorv1.GoInvocationConfig{}
		c.SetPackages([]string{"./..."})
		return c
	}
	t.Run("ambient exec substitution", func(t *testing.T) {
		neutralAmbient(t)
		t.Setenv("GOFLAGS", "-exec=/bin/true")
		if _, err := NormalizeInvocation(context.Background(), dir, goInvocation("x", cfg())); err == nil || !strings.Contains(err.Error(), "unsupported") {
			t.Fatalf("ambient -exec accepted: %v", err)
		}
	})
	t.Run("ambient selection shaping", func(t *testing.T) {
		neutralAmbient(t)
		t.Setenv("GOFLAGS", "-run=NoSuchTestAtAll")
		if _, err := NormalizeInvocation(context.Background(), dir, goInvocation("x", cfg())); err == nil || !strings.Contains(err.Error(), "shapes test selection") {
			t.Fatalf("ambient -run accepted: %v", err)
		}
	})
	t.Run("ambient overlay", func(t *testing.T) {
		neutralAmbient(t)
		t.Setenv("GOFLAGS", "-overlay=/tmp/overlay.json")
		_, err := NormalizeInvocation(context.Background(), dir, goInvocation("x", cfg()))
		if err == nil || !strings.Contains(err.Error(), "unsupported") {
			t.Fatalf("ambient -overlay accepted: %v", err)
		}
	})
	t.Run("ambient owned flag", func(t *testing.T) {
		neutralAmbient(t)
		t.Setenv("GOFLAGS", "-count=1")
		_, err := NormalizeInvocation(context.Background(), dir, goInvocation("x", cfg()))
		if err == nil || !strings.Contains(err.Error(), "owned by") {
			t.Fatalf("ambient owned -count accepted: %v", err)
		}
	})
	t.Run("ambient package driver", func(t *testing.T) {
		neutralAmbient(t)
		t.Setenv("GOPACKAGESDRIVER", "/usr/bin/fancy-driver")
		_, err := NormalizeInvocation(context.Background(), dir, goInvocation("x", cfg()))
		if err == nil || !strings.Contains(err.Error(), "package driver") {
			t.Fatalf("ambient package driver accepted: %v", err)
		}
	})
}

// TestGoNormalizeWorkspaceModeRequiresDeclaration pins that an explicit
// WORKSPACE mode in a tree without go.work is refused, not defaulted.
func TestGoNormalizeWorkspaceModeRequiresDeclaration(t *testing.T) {
	stipulate.Covers(t, "REQ-go-workspace")
	neutralAmbient(t)
	dir, err := filepath.Abs(filepath.Join("testdata", "policyderive", "single"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./..."})
	cfg.SetWorkspaceMode(stipulatorv1.GoWorkspaceMode_GO_WORKSPACE_MODE_WORKSPACE)
	_, err = NormalizeInvocation(context.Background(), dir, goInvocation("ws", cfg))
	if err == nil || !strings.Contains(err.Error(), "declares no go.work") {
		t.Fatalf("workspace mode without declaration accepted: %v", err)
	}
}

// The normalized invocation carries all four observation-guard roots
// resolved from the effective environment — toolchain, module cache,
// build cache, and the producing environment's temp root — so witness
// observation can classify reads under them instead of sealing. The
// build cache is sourced from the go env config file, the ambient
// source GOENV=off silences in the child: the resolved value must be
// pinned into the frozen environment, or the declared root and the
// child's actual cache silently diverge.
func TestGoNormalizeCarriesObservationGuardRoots(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness")
	neutralAmbient(t)
	gocache := t.TempDir()
	tmproot := t.TempDir()
	goenvFile := filepath.Join(t.TempDir(), "goenv")
	if err := os.WriteFile(goenvFile, []byte("GOCACHE="+gocache+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOENV", goenvFile)
	t.Setenv("TMPDIR", tmproot)
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./..."})
	n, err := NormalizeInvocation(context.Background(), discoverFixture(t), goInvocation("roots", cfg))
	if err != nil {
		t.Fatal(err)
	}
	if pinned, ok := lookupEnv(n.Env, "GOCACHE"); !ok || pinned != gocache {
		t.Errorf("child env GOCACHE = %q (present=%v), want the resolved %q pinned — GOENV=off silences the config file the value came from", pinned, ok, gocache)
	}
	// The temp root, like every classification root, is the facade's to
	// resolve from the witness environment: TMPDIR rides it verbatim.
	if got, ok := lookupEnv(witnessEnvOf(n), "TMPDIR"); !ok || got != tmproot {
		t.Errorf("witness env TMPDIR = %q (present=%v), want the effective %q", got, ok, tmproot)
	}
}

// Bracket paths admit exactly the forms the observation bracket
// accepts — clean absolute or clean tree-relative slash paths, never a
// parent traversal — and ride the normalized invocation.
func TestGoNormalizeBracketPaths(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness")
	neutralAmbient(t)
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./..."})
	cfg.SetBracketPaths([]string{"/bin/sh", "testdata/pinned.bin", "testdata/a..b.golden"})
	n, err := NormalizeInvocation(context.Background(), discoverFixture(t), goInvocation("bp", cfg))
	if err != nil {
		t.Fatal(err)
	}
	if len(n.BracketPaths) != 3 || n.BracketPaths[0] != "/bin/sh" || n.BracketPaths[2] != "testdata/a..b.golden" {
		t.Fatalf("BracketPaths = %v", n.BracketPaths)
	}
	// A malformed bracket path is a record fault: it refuses before any
	// toolchain spawn (REQ-check-preparation). The hook's positive
	// control first: a well-formed record does spawn through it.
	spawns := 0
	commandHook = func(string, []string) { spawns++ }
	defer func() { commandHook = nil }()
	if _, err := NormalizeInvocation(context.Background(), discoverFixture(t), goInvocation("bp", cfg)); err != nil {
		t.Fatal(err)
	}
	if spawns == 0 {
		t.Fatal("a well-formed record spawned nothing through the seam; the zero-spawn pins below would be vacuous")
	}
	spawns = 0
	for _, bad := range []string{"", "/bin/../sh", "/unclean//sh", "a/../b", "./rel", "../escape", ".."} {
		c := &stipulatorv1.GoInvocationConfig{}
		c.SetPackages([]string{"./..."})
		c.SetBracketPaths([]string{bad})
		if _, err := NormalizeInvocation(context.Background(), discoverFixture(t), goInvocation("bp", c)); err == nil {
			t.Errorf("bracket path %q accepted", bad)
		}
	}
	if spawns != 0 {
		t.Fatalf("malformed bracket paths cost %d toolchain spawns before refusing", spawns)
	}
}

func TestGoNormalizeExcludedPaths(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness")
	neutralAmbient(t)
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./..."})
	cfg.SetExcludedPaths([]string{".claude", "tmp/session"})
	n, err := NormalizeInvocation(context.Background(), discoverFixture(t), goInvocation("ex", cfg))
	if err != nil {
		t.Fatal(err)
	}
	if len(n.ExcludedPaths) != 2 || n.ExcludedPaths[0] != ".claude" || n.ExcludedPaths[1] != "tmp/session" {
		t.Fatalf("ExcludedPaths = %v", n.ExcludedPaths)
	}
	tmpAbs := filepath.Join(discoverFixture(t), "sub")
	for _, bad := range []string{"", "a/../b", "./rel", "../escape", "/unclean//x", "a\x01b", tmpAbs} {
		c := &stipulatorv1.GoInvocationConfig{}
		c.SetPackages([]string{"./..."})
		c.SetExcludedPaths([]string{bad})
		if _, err := NormalizeInvocation(context.Background(), discoverFixture(t), goInvocation("ex", c)); err == nil {
			t.Errorf("excluded path %q accepted", bad)
		}
	}
}

// Vouch entries canonicalize (sorted, deduplicated) and malformed
// identities refuse at policy acceptance: a bare package would silently
// confer nothing (REQ-evidence-witness-freshness's vouch discipline).
func TestGoNormalizeVouchesCanonicalizeAndRefuse(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness")
	neutralAmbient(t)
	dir := discoverFixture(t)
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./..."})
	cfg.SetRace(true)
	cfg.SetDynamicStateVouches([]*stipulatorv1.DynamicStateVouch{
		vouchEntry("b.example/dep", "Var"), vouchEntry("a.example/dep", "Var"), vouchEntry("b.example/dep", "Var"),
	})
	n, err := NormalizeInvocation(context.Background(), dir, goInvocation("vouched", cfg))
	if err != nil {
		t.Fatal(err)
	}
	if len(n.Vouches) != 2 || n.Vouches[0] != "a.example/dep.Var" || n.Vouches[1] != "b.example/dep.Var" {
		t.Fatalf("vouches = %v, want sorted deduplicated pair", n.Vouches)
	}
	for _, bad := range []*stipulatorv1.DynamicStateVouch{
		vouchEntry("", "Var"),
		vouchEntry("a.example/dep", ""),
		vouchEntry("a.example/dep\n", "Var"),
		vouchEntry("a.example/dep ", "Var"),
		vouchEntry("a.example/dep\x01b.example/dep", "Var"),
		vouchEntry("a.example/dep", "not-an-identifier"),
		vouchEntry("a.example/dep", "9lives"),
		vouchEntry("a.example/dep", "Var.Sub"),
		vouchEntry("a.example/dep", "Var "),
	} {
		cfg := &stipulatorv1.GoInvocationConfig{}
		cfg.SetPackages([]string{"./..."})
		cfg.SetRace(true)
		cfg.SetDynamicStateVouches([]*stipulatorv1.DynamicStateVouch{bad})
		spawns := 0
		commandHook = func(string, []string) { spawns++ }
		_, err := NormalizeInvocation(context.Background(), dir, goInvocation("bad", cfg))
		commandHook = nil
		if err == nil || !strings.Contains(err.Error(), "dynamic_state_vouches") {
			t.Fatalf("malformed vouch %+v accepted: %v", bad, err)
		}
		// The record decides the refusal: no toolchain spawn precedes it
		// (evidence.md: "refuses at policy acceptance").
		if spawns != 0 {
			t.Fatalf("malformed vouch %+v cost %d toolchain spawns before refusing", bad, spawns)
		}
	}
}

// A malformed excluded path — empty, control-bearing, traversing, or
// unclean — is a record fault refused before any toolchain spawn; only
// an absolute path's position against the tree waits for normalization
// (REQ-check-preparation).
func TestGoNormalizeExcludedPathFormsRefuseBeforeAnySpawn(t *testing.T) {
	stipulate.Covers(t, "REQ-check-preparation")
	neutralAmbient(t)
	spawns := 0
	commandHook = func(string, []string) { spawns++ }
	defer func() { commandHook = nil }()
	for _, bad := range []string{"", "a\x01b", "../x", "./x", "/unclean//x"} {
		cfg := &stipulatorv1.GoInvocationConfig{}
		cfg.SetPackages([]string{"./..."})
		cfg.SetExcludedPaths([]string{bad})
		if _, err := NormalizeInvocation(context.Background(), discoverFixture(t), goInvocation("ep", cfg)); err == nil {
			t.Errorf("excluded path %q accepted", bad)
		}
	}
	if spawns != 0 {
		t.Fatalf("malformed excluded paths cost %d toolchain spawns before refusing", spawns)
	}
}

func vouchEntry(pkg, variable string) *stipulatorv1.DynamicStateVouch {
	v := &stipulatorv1.DynamicStateVouch{}
	v.SetPackage(pkg)
	v.SetVariable(variable)
	return v
}

// One flag construction describes the binary the witnesses run as: the
// selection, the module mode, and the profile, shared by the engine's
// loads and the witness command (the latter resolving a committed
// profile against the tree root, since the child runs in its module).
func TestBuildFlagsCarryModeAndProfile(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness")
	got := buildFlags(true, []string{"a", "b"}, stipulatorv1.GoModuleMode_GO_MODULE_MODE_VENDOR, "prof.pgo")
	want := []string{"-race", "-tags=a,b", "-mod=vendor", "-pgo=prof.pgo"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("buildFlags = %v, want %v", got, want)
	}
	if bare := buildFlags(false, nil, stipulatorv1.GoModuleMode_GO_MODULE_MODE_UNSPECIFIED, ""); len(bare) != 0 {
		t.Fatalf("bare selection = %v, want none", bare)
	}
	n := &NormalizedInvocation{Dir: filepath.Join(string(filepath.Separator), "tree", "sub"), ModuleRoot: "sub", Race: true, ModuleMode: stipulatorv1.GoModuleMode_GO_MODULE_MODE_VENDOR, PGO: "prof.pgo"}
	args := strings.Join(testCommandArgs(n, "example.com/p", nil, ""), " ")
	for _, flag := range []string{"-race", "-mod=vendor", "-pgo=" + filepath.Join(string(filepath.Separator), "tree", "prof.pgo")} {
		if !strings.Contains(args, " "+flag+" ") && !strings.HasSuffix(args, " "+flag) {
			t.Fatalf("witness command %q lacks %q", args, flag)
		}
	}
}

// TestEnvHelpersFollowGofreshsPolicy pins the invocation's environment
// helpers to gofresh's one policy: setEnv keeps gofresh's key order (a
// whole-entry sort would put "A-=1" before "A=1"), replaces under the
// platform's key rule, and lookupEnv reads under it — one key-equality
// decision for the normalizer, the report, and the engine
// (REQ-evidence-flip-environment).
//
//gofresh:pure
func TestEnvHelpersFollowGofreshsPolicy(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-flip-environment")
	got := setEnv([]string{"A-=1", "B=2"}, "A", "1")
	if want := []string{"A=1", "A-=1", "B=2"}; !slices.Equal(got, want) {
		t.Fatalf("setEnv order = %q, want gofresh's key order %q", got, want)
	}
	got = setEnv(got, "A", "3")
	if want := []string{"A=3", "A-=1", "B=2"}; !slices.Equal(got, want) {
		t.Fatalf("setEnv replace = %q, want %q", got, want)
	}
	if v, ok := lookupEnv(got, "A-"); !ok || v != "1" {
		t.Fatalf("lookupEnv(A-) = %q,%v", v, ok)
	}
	if runtime.GOOS != "windows" {
		// Case-distinct keys are distinct variables here; on windows
		// gofresh folds them to one identity, and these helpers ride
		// its rule rather than restating one.
		got = setEnv(got, "a", "x")
		if _, ok := lookupEnv(got, "A"); !ok || len(got) != 4 {
			t.Fatalf("setEnv(a) on a case-sensitive platform = %q, want A kept beside a", got)
		}
		if got = dropEnv(got, "a"); len(got) != 3 {
			t.Fatalf("dropEnv(a) = %q, want A kept", got)
		}
	}
}

// TestSetEnvKeepsGofreshsOrder pins the setter's order to gofresh's
// own: over random normalized environments and entries, inserting by
// setEnv yields exactly what gotool.NormalizeEnv yields over the same
// entries — the one order every consumer of a normalized environment
// reads, restated in envEntryLess until gotool carries the setter; the
// drawn keys are deduplicated under the platform's rule, so the
// property holds on a case-folding platform too
// (REQ-evidence-flip-environment).
//
//gofresh:pure
func TestSetEnvKeepsGofreshsOrder(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-flip-environment")
	key := rapid.SampledFrom([]string{"A", "A-", "AB", "a", "B", "Z", "GO_X", "GOX"})
	value := rapid.SampledFrom([]string{"", "1", "x=y", "a b"})
	rapid.Check(t, func(rt *rapid.T) {
		var entries []string
		var seen declaredEnvKeys
		for _, k := range rapid.SliceOfN(key, 0, 6).Draw(rt, "keys") {
			if !seen.holds(k) {
				seen = append(seen, k)
				entries = append(entries, k+"="+value.Draw(rt, "v"))
			}
		}
		env, err := gotool.NormalizeEnv(entries)
		if err != nil {
			rt.Fatal(err)
		}
		k, v := key.Draw(rt, "key"), value.Draw(rt, "value")
		want, err := gotool.NormalizeEnv(append(dropEnv(env, k), k+"="+v))
		if err != nil {
			rt.Fatal(err)
		}
		if got := setEnv(env, k, v); !slices.Equal(got, want) {
			rt.Fatalf("setEnv(%q, %q, %q) = %q, want gofresh's order %q", env, k, v, got, want)
		}
	})
}

// TestDriverPinIsTheCuratedEnvironmentsLastWord pins REQ-go-owned-processes'
// pin: a declared environment or denial cannot reopen the package
// driver — the curated environment carries GOPACKAGESDRIVER=off after
// every declaration. A pinned key spelled under another case is judged
// by the platform's rule: refused at policy acceptance where the
// platform folds case, and a distinct variable elsewhere, beside which
// the pin still stands last.
func TestDriverPinIsTheCuratedEnvironmentsLastWord(t *testing.T) {
	stipulate.Covers(t, "REQ-go-owned-processes")
	variant := validateEnvOverrides([]string{"gopackagesdriver=/x"})
	denial := validateEnvDeny([]string{"Gopackagesdriver"})
	if runtime.GOOS == "windows" {
		if variant == nil || denial == nil {
			t.Fatalf("a case-variant spelling of the pinned driver key was accepted on a case-folding platform: %v / %v", variant, denial)
		}
		return
	}
	if variant != nil || denial != nil {
		t.Fatalf("a distinct case-variant key was refused on a case-sensitive platform: %v / %v", variant, denial)
	}
	neutralAmbient(t)
	dir := writeModule(t, map[string]string{"go.mod": "module example.com/pin\n\ngo 1.26\n", "p.go": "package pin\n"})
	c := &stipulatorv1.GoInvocationConfig{}
	c.SetPackages([]string{"./..."})
	c.SetEnvironment([]string{"gopackagesdriver=/x"})
	n, err := NormalizeInvocation(context.Background(), dir, goInvocation("x", c))
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := lookupEnv(n.Env, "GOPACKAGESDRIVER"); !ok || v != "off" {
		t.Fatalf("curated GOPACKAGESDRIVER = %q,%v; want off as the last word", v, ok)
	}
	if v, ok := lookupEnv(n.Env, "gopackagesdriver"); !ok || v != "/x" {
		t.Fatalf("the distinct lowercase variable = %q,%v; want the declaration kept beside the pin", v, ok)
	}
}

// TestDriverAttributionNamesOnlyARealDriver pins the refusal's
// attribution: an inherited environment refused for a malformed entry
// while GOPACKAGESDRIVER=off is set names the entry, never the driver
// (REQ-go-owned-processes).
func TestDriverAttributionNamesOnlyARealDriver(t *testing.T) {
	stipulate.Covers(t, "REQ-go-owned-processes")
	neutralAmbient(t)
	t.Setenv("GOPACKAGESDRIVER", "off")
	dir := writeModule(t, map[string]string{"go.mod": "module example.com/pin\n\ngo 1.26\n", "p.go": "package pin\n"})
	c := &stipulatorv1.GoInvocationConfig{}
	c.SetPackages([]string{"./..."})
	// An ambient entry with no '=' — the shape execve carries into a
	// process and no Setenv can produce — refused by gofresh's
	// normalization, unrelated to the driver.
	swapAmbientEnviron(t, func() []string { return append(os.Environ(), "NOEQUALS") })
	_, err := NormalizeInvocation(context.Background(), dir, goInvocation("x", c))
	if err == nil || strings.Contains(err.Error(), "package driver") || !strings.Contains(err.Error(), "inherited environment") {
		t.Fatalf("a malformed inherited entry beside GOPACKAGESDRIVER=off = %v; want the entry named, not the driver", err)
	}
}

// TestWorkspaceQueryRefusesAMalformedAmbientEnvironment pins the
// workspace query's environment to gofresh's policy at its source: an
// inherited entry gofresh's normalization refuses is refused here,
// naming the entry, before the go.work query spawns under it
// (REQ-go-owned-processes).
func TestWorkspaceQueryRefusesAMalformedAmbientEnvironment(t *testing.T) {
	stipulate.Covers(t, "REQ-go-owned-processes")
	neutralAmbient(t)
	swapAmbientEnviron(t, func() []string { return append(os.Environ(), "NOEQUALS") })
	_, err := goworkEnv(t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "inherited environment") {
		t.Fatalf("goworkEnv over a malformed ambient entry = %v; want the entry refused", err)
	}
}

// swapAmbientEnviron installs an inherited-environment read for the
// test's lifetime.
func swapAmbientEnviron(t *testing.T, read func() []string) {
	t.Helper()
	prior := ambientEnviron
	ambientEnviron = read
	t.Cleanup(func() { ambientEnviron = prior })
}
