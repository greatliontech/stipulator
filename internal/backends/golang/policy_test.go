package golang

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/policy"
	"github.com/greatliontech/stipulator/stipulate"
)

var updatePolicyGolden = flag.Bool("update-policy-golden", false, "rewrite the derived-policy goldens under testdata/policyderive")

// TestGoPolicyDeriveGoldenByteIdentical pins the policy-init derivation
// byte-for-byte against committed goldens: the derived record depends only
// on the tree's workspace declaration — module roots tree-relative in
// slash form, never host paths — so any two hosts derive identical bytes
// from the same fixture.
func TestGoPolicyDeriveGoldenByteIdentical(t *testing.T) {
	stipulate.Covers(t, "REQ-policy-explicit")
	stipulate.Covers(t, "REQ-policy-record-location")
	cases := []struct {
		name, dir, golden string
	}{
		{"workspace", "workspace", "workspace.golden.textproto"},
		{"single module without go.work", "single", "single.golden.textproto"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := filepath.Join("testdata", "policyderive", c.dir)
			p, err := DerivePolicy(dir)
			if err != nil {
				t.Fatal(err)
			}
			rendered, err := policy.Render(p)
			if err != nil {
				t.Fatal(err)
			}
			goldenPath := filepath.Join("testdata", "policyderive", c.golden)
			if *updatePolicyGolden {
				if err := os.WriteFile(goldenPath, rendered, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(rendered, want) {
				t.Errorf("derived policy drifted from %s:\n%s", goldenPath, rendered)
			}
			if abs, err := filepath.Abs(dir); err == nil && bytes.Contains(rendered, []byte(abs)) {
				t.Errorf("derived policy embeds the host path %q", abs)
			}
			// The derivation must be consumable through the same seam
			// every consumer loads through: strict-parse the golden back
			// and dispatch it.
			parsed, err := policy.Parse(want)
			if err != nil {
				t.Fatalf("golden does not strict-parse: %v", err)
			}
			if !proto.Equal(parsed, p) {
				t.Error("golden parses back to a different policy than the derivation")
			}
			if _, err := policy.Dispatch(parsed, map[string]policy.Backend{"go": Policy{}}); err != nil {
				t.Fatalf("golden does not dispatch: %v", err)
			}
		})
	}
}

// TestGoPolicyDeriveMirrorsLegacySuite pins the derivation's shape against
// what the legacy universal suite executes: one race-enabled ./...
// invocation per workspace member, each carrying the legacy explicit
// 30-minute per-binary timeout — dropping it would leave binaries bounded
// only by the invocation envelope, not what the legacy suite ran — under
// the derived invocation envelope.
func TestGoPolicyDeriveMirrorsLegacySuite(t *testing.T) {
	stipulate.Covers(t, "REQ-policy-explicit")
	p, err := DerivePolicy(filepath.Join("testdata", "policyderive", "workspace"))
	if err != nil {
		t.Fatal(err)
	}
	invs := p.GetInvocations()
	if len(invs) != 3 {
		t.Fatalf("invocations = %d, want one per go.work member", len(invs))
	}
	wantRoots := []string{"", "member/nested", "zeta"}
	for i, inv := range invs {
		cfg := inv.GetGo()
		if cfg == nil {
			t.Fatalf("invocation %q carries no Go payload", inv.GetName())
		}
		if got := cfg.GetModuleRoot(); got != wantRoots[i] {
			t.Errorf("invocation %d module_root = %q, want %q", i, got, wantRoots[i])
		}
		if got := cfg.GetPackages(); len(got) != 1 || got[0] != "./..." {
			t.Errorf("invocation %q packages = %v, want [./...]", inv.GetName(), got)
		}
		if !cfg.GetRace() {
			t.Errorf("invocation %q is not race-enabled; the legacy suite races everything", inv.GetName())
		}
		if got := cfg.GetArgs(); len(got) != 1 || got[0] != "-test.timeout=30m" {
			t.Errorf("invocation %q args = %v, want the legacy per-binary [-test.timeout=30m]", inv.GetName(), got)
		}
		if want := durationpb.New(derivedTimeout); !proto.Equal(inv.GetTimeout(), want) {
			t.Errorf("invocation %q timeout = %v, want %v", inv.GetName(), inv.GetTimeout(), want)
		}
	}
}

// TestGoPolicyDeriveRefusesEscapingMember pins the hermeticity refusal at
// derivation: a go.work member outside the tree never reaches a record.
func TestGoPolicyDeriveRefusesEscapingMember(t *testing.T) {
	stipulate.Covers(t, "REQ-go-workspace")
	_, err := DerivePolicy(filepath.Join("testdata", "policyderive", "escape"))
	if err == nil {
		t.Fatal("escaping go.work member accepted")
	}
	if !strings.Contains(err.Error(), "escapes the verification tree") {
		t.Fatalf("error = %q, want it to name the tree escape", err)
	}
}

// TestGoPolicyPayloadValidation pins the dispatch-time payload checks: the
// Go backend claims only its own typed payload, and a module root that is
// absolute, non-canonical, or escaping the tree refuses the invocation.
func TestGoPolicyPayloadValidation(t *testing.T) {
	stipulate.Covers(t, "REQ-go-workspace")
	mk := func(root string) *stipulatorv1.GoInvocationConfig {
		cfg := &stipulatorv1.GoInvocationConfig{}
		if root != "" {
			cfg.SetModuleRoot(root)
		}
		cfg.SetPackages([]string{"./..."})
		cfg.SetRace(true)
		return cfg
	}
	accept := []string{"", "bindingsurface", "stipulate/structural"}
	for _, root := range accept {
		if err := (Policy{}).ValidateInvocation("race", mk(root)); err != nil {
			t.Errorf("module_root %q refused: %v", root, err)
		}
	}
	reject := []struct{ root, wantErr string }{
		{`..\..\x`, "host-specific path runes"},
		{`C:/evil`, "host-specific path runes"},
		{`C:\evil`, "host-specific path runes"},
		{"..", "escapes the verification tree"},
		{"../sibling", "escapes the verification tree"},
		{"a/../../b", "escapes the verification tree"},
		{"/abs", "is absolute"},
		{".", "canonical slash form"},
		{"./x", "canonical slash form"},
		{"a//b", "canonical slash form"},
		{"a/./b", "canonical slash form"},
		{"a/b/", "canonical slash form"},
		{"a/..", "canonical slash form"},
	}
	for _, c := range reject {
		err := (Policy{}).ValidateInvocation("race", mk(c.root))
		if err == nil {
			t.Errorf("module_root %q accepted", c.root)
			continue
		}
		if !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("module_root %q: error = %q, want it to name %q", c.root, err, c.wantErr)
		}
	}
	if err := (Policy{}).ValidateInvocation("race", &stipulatorv1.PolicyInvocation{}); err == nil {
		t.Error("foreign payload type accepted; the Go backend must claim only GoInvocationConfig")
	}
}

// TestGoPolicyConfigStaticValidation pins the record-only validation of
// the full normalization surface: every typed field refuses malformed,
// escaping, flag-injecting, or double-sourced configuration before any
// toolchain work happens.
func TestGoPolicyConfigStaticValidation(t *testing.T) {
	stipulate.Covers(t, "REQ-policy-explicit")
	base := func() *stipulatorv1.GoInvocationConfig {
		cfg := &stipulatorv1.GoInvocationConfig{}
		cfg.SetPackages([]string{"./..."})
		cfg.SetRace(true)
		return cfg
	}
	accept := []func(*stipulatorv1.GoInvocationConfig){
		func(c *stipulatorv1.GoInvocationConfig) {},
		func(c *stipulatorv1.GoInvocationConfig) { c.SetToolchain("go1.26.4") },
		func(c *stipulatorv1.GoInvocationConfig) { c.SetEnvironment([]string{"GOPROXY=off", "HOME=/tmp/h"}) },
		func(c *stipulatorv1.GoInvocationConfig) { c.SetEnvDeny([]string{"GOPROXY"}) },
		func(c *stipulatorv1.GoInvocationConfig) { c.SetGoos("linux"); c.SetGoarch("arm64") },
		func(c *stipulatorv1.GoInvocationConfig) { c.SetTags([]string{"integration", "special"}) },
		func(c *stipulatorv1.GoInvocationConfig) { c.SetGoflags("-trimpath -v") },
		func(c *stipulatorv1.GoInvocationConfig) {
			c.SetModuleMode(stipulatorv1.GoModuleMode_GO_MODULE_MODE_VENDOR)
		},
		func(c *stipulatorv1.GoInvocationConfig) { c.SetPgo("profiles/default.pgo") },
		func(c *stipulatorv1.GoInvocationConfig) { c.SetPgo("off") },
		func(c *stipulatorv1.GoInvocationConfig) { c.SetCount(3) },
		func(c *stipulatorv1.GoInvocationConfig) {
			c.SetCacheMode(stipulatorv1.GoCacheMode_GO_CACHE_MODE_BYPASS)
			c.SetCount(1)
		},
		func(c *stipulatorv1.GoInvocationConfig) { c.SetArgs([]string{"-quick", "positional"}) },
		func(c *stipulatorv1.GoInvocationConfig) { c.SetPackages([]string{"example.com/disc/..."}) },
	}
	for i, mutate := range accept {
		cfg := base()
		mutate(cfg)
		if err := (Policy{}).ValidateInvocation("race", cfg); err != nil {
			t.Errorf("accept case %d refused: %v", i, err)
		}
	}
	reject := []struct {
		name, wantErr string
		mutate        func(*stipulatorv1.GoInvocationConfig)
	}{
		{"flag-shaped package", "flag-shaped", func(c *stipulatorv1.GoInvocationConfig) { c.SetPackages([]string{"-run=."}) }},
		{"empty package", "empty", func(c *stipulatorv1.GoInvocationConfig) { c.SetPackages([]string{""}) }},
		{"escaping package", "escapes", func(c *stipulatorv1.GoInvocationConfig) { c.SetPackages([]string{"../sibling/..."}) }},
		{"absolute package", "absolute", func(c *stipulatorv1.GoInvocationConfig) { c.SetPackages([]string{"/abs/..."}) }},
		{"toolchain with space", "bare token", func(c *stipulatorv1.GoInvocationConfig) { c.SetToolchain("go1.26.4 local") }},
		{"environment pinned key", "backend-pinned", func(c *stipulatorv1.GoInvocationConfig) { c.SetEnvironment([]string{"GOFLAGS=-v"}) }},
		{"environment malformed", "KEY=VALUE", func(c *stipulatorv1.GoInvocationConfig) { c.SetEnvironment([]string{"NOEQUALS"}) }},
		{"environment duplicate", "duplicate", func(c *stipulatorv1.GoInvocationConfig) { c.SetEnvironment([]string{"A=1", "A=2"}) }},
		{"env_deny pinned key", "backend-pinned", func(c *stipulatorv1.GoInvocationConfig) { c.SetEnvDeny([]string{"GOWORK"}) }},
		{"env_deny with equals", "bare variable name", func(c *stipulatorv1.GoInvocationConfig) { c.SetEnvDeny([]string{"A=1"}) }},
		{"env_deny duplicate", "duplicate", func(c *stipulatorv1.GoInvocationConfig) { c.SetEnvDeny([]string{"A", "A"}) }},
		{"empty goos", "bare token", func(c *stipulatorv1.GoInvocationConfig) { c.SetGoos("") }},
		{"tag with comma", "build tag", func(c *stipulatorv1.GoInvocationConfig) { c.SetTags([]string{"a,b"}) }},
		{"flag-shaped tag", "build tag", func(c *stipulatorv1.GoInvocationConfig) { c.SetTags([]string{"-special"}) }},
		{"goflags overlay", "unsupported", func(c *stipulatorv1.GoInvocationConfig) { c.SetGoflags("-overlay=/tmp/o.json") }},
		{"goflags toolexec", "unsupported", func(c *stipulatorv1.GoInvocationConfig) { c.SetGoflags("-toolexec=strace") }},
		{"goflags exec", "unsupported", func(c *stipulatorv1.GoInvocationConfig) { c.SetGoflags("-exec=/bin/true") }},
		{"goflags ldflags", "unsupported", func(c *stipulatorv1.GoInvocationConfig) { c.SetGoflags("-ldflags=-X main.v=1") }},
		{"goflags run selection", "shapes test selection", func(c *stipulatorv1.GoInvocationConfig) { c.SetGoflags("-run=TestOnlyThis") }},
		{"goflags skip selection", "shapes test selection", func(c *stipulatorv1.GoInvocationConfig) { c.SetGoflags("-skip=.*") }},
		{"goflags short selection", "shapes test selection", func(c *stipulatorv1.GoInvocationConfig) { c.SetGoflags("-short") }},
		{"goflags fuzz selection", "shapes test selection", func(c *stipulatorv1.GoInvocationConfig) { c.SetGoflags("-fuzz=FuzzX") }},
		{"goflags owned race", "owned by", func(c *stipulatorv1.GoInvocationConfig) { c.SetGoflags("-race") }},
		{"goflags owned tags", "owned by", func(c *stipulatorv1.GoInvocationConfig) { c.SetGoflags("-tags=x") }},
		{"goflags owned timeout", "owned by", func(c *stipulatorv1.GoInvocationConfig) { c.SetGoflags("-timeout=1h") }},
		{"pgo escape", "pgo", func(c *stipulatorv1.GoInvocationConfig) { c.SetPgo("../prof.pgo") }},
		{"pgo empty", "pgo", func(c *stipulatorv1.GoInvocationConfig) { c.SetPgo("") }},
		{"zero count", "positive", func(c *stipulatorv1.GoInvocationConfig) { c.SetCount(0) }},
		{"negative count", "positive", func(c *stipulatorv1.GoInvocationConfig) { c.SetCount(-1) }},
		{"bypass with count", "incompatible", func(c *stipulatorv1.GoInvocationConfig) {
			c.SetCacheMode(stipulatorv1.GoCacheMode_GO_CACHE_MODE_BYPASS)
			c.SetCount(2)
		}},
		{"args NUL", "NUL", func(c *stipulatorv1.GoInvocationConfig) { c.SetArgs([]string{"a\x00b"}) }},
		{"args testlogfile", "capture file", func(c *stipulatorv1.GoInvocationConfig) {
			c.SetArgs([]string{"-test.testlogfile=/dev/null"})
		}},
		{"args testlogfile split value", "capture file", func(c *stipulatorv1.GoInvocationConfig) {
			c.SetArgs([]string{"-test.testlogfile", "/dev/null"})
		}},
		{"args testlogfile double dash", "capture file", func(c *stipulatorv1.GoInvocationConfig) {
			c.SetArgs([]string{"--test.testlogfile=/dev/null"})
		}},
		{"args test.run", "test selection", func(c *stipulatorv1.GoInvocationConfig) {
			c.SetArgs([]string{"-test.run=TestOnlyThis"})
		}},
		{"args test.run split value", "test selection", func(c *stipulatorv1.GoInvocationConfig) {
			c.SetArgs([]string{"-test.run", "TestOnlyThis"})
		}},
		{"args test.run double dash", "test selection", func(c *stipulatorv1.GoInvocationConfig) {
			c.SetArgs([]string{"--test.run=TestOnlyThis"})
		}},
		{"args test.skip", "test selection", func(c *stipulatorv1.GoInvocationConfig) {
			c.SetArgs([]string{"-test.skip=TestSkipThis"})
		}},
		{"args test.skip split value", "test selection", func(c *stipulatorv1.GoInvocationConfig) {
			c.SetArgs([]string{"-test.skip", "TestSkipThis"})
		}},
		{"args test.list double dash", "test selection", func(c *stipulatorv1.GoInvocationConfig) {
			c.SetArgs([]string{"--test.list=.*"})
		}},
		{"args test.bench", "test selection", func(c *stipulatorv1.GoInvocationConfig) {
			c.SetArgs([]string{"-test.bench=."})
		}},
		{"args test.fuzz", "test selection", func(c *stipulatorv1.GoInvocationConfig) {
			c.SetArgs([]string{"-test.fuzz=FuzzX"})
		}},
		{"args bare run", "test selection", func(c *stipulatorv1.GoInvocationConfig) {
			c.SetArgs([]string{"-run=TestOnlyThis"})
		}},
	}
	for _, c := range reject {
		cfg := base()
		c.mutate(cfg)
		err := (Policy{}).ValidateInvocation("race", cfg)
		if err == nil {
			t.Errorf("%s: accepted", c.name)
			continue
		}
		if !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("%s: error = %q, want it to name %q", c.name, err, c.wantErr)
		}
	}
}
