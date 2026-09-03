package golang

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/protobuf/types/known/durationpb"

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

// witness_concurrency is positive-when-present: an explicit zero (or
// negative) refuses normalization instead of silently meaning default.
func TestGoNormalizeWitnessConcurrencyPositiveWhenPresent(t *testing.T) {
	stipulate.Covers(t, "REQ-policy-explicit")
	neutralAmbient(t)
	zero := &stipulatorv1.GoInvocationConfig{}
	zero.SetPackages([]string{"./..."})
	zero.SetWitnessConcurrency(0)
	if _, err := NormalizeInvocation(context.Background(), discoverFixture(t), goInvocation("wc", zero)); err == nil {
		t.Error("witness_concurrency 0 accepted; positive-when-present must refuse")
	}
	ok := &stipulatorv1.GoInvocationConfig{}
	ok.SetPackages([]string{"./..."})
	ok.SetWitnessConcurrency(4)
	n, err := NormalizeInvocation(context.Background(), discoverFixture(t), goInvocation("wc", ok))
	if err != nil || n.WitnessConcurrency != 4 {
		t.Fatalf("explicit bound not carried: %v %v", n, err)
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
	for _, bad := range []string{"", "/bin/../sh", "/unclean//sh", "a/../b", "./rel", "../escape", ".."} {
		c := &stipulatorv1.GoInvocationConfig{}
		c.SetPackages([]string{"./..."})
		c.SetBracketPaths([]string{bad})
		if _, err := NormalizeInvocation(context.Background(), discoverFixture(t), goInvocation("bp", c)); err == nil {
			t.Errorf("bracket path %q accepted", bad)
		}
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
		if _, err := NormalizeInvocation(context.Background(), dir, goInvocation("bad", cfg)); err == nil || !strings.Contains(err.Error(), "dynamic_state_vouches") {
			t.Fatalf("malformed vouch %+v accepted: %v", bad, err)
		}
	}
}

func vouchEntry(pkg, variable string) *stipulatorv1.DynamicStateVouch {
	v := &stipulatorv1.DynamicStateVouch{}
	v.SetPackage(pkg)
	v.SetVariable(variable)
	return v
}
