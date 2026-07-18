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
// invocation per workspace member under the legacy 30-minute ceiling.
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
