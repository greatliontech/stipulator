package policy

import (
	"strings"
	"testing"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/stipulate"
	"google.golang.org/protobuf/types/known/durationpb"
)

func mkInvocation(name string, timeout *durationpb.Duration, withConfig bool) *stipulatorv1.PolicyInvocation {
	inv := &stipulatorv1.PolicyInvocation{}
	inv.SetName(name)
	if timeout != nil {
		inv.SetTimeout(timeout)
	}
	if withConfig {
		cfg := &stipulatorv1.GoInvocationConfig{}
		cfg.SetPackages([]string{"./..."})
		cfg.SetRace(true)
		inv.SetGo(cfg)
	}
	return inv
}

func mkPolicy(invs ...*stipulatorv1.PolicyInvocation) *stipulatorv1.TestPolicy {
	p := &stipulatorv1.TestPolicy{}
	p.SetInvocations(invs)
	return p
}

//gofresh:pure
func TestPolicyCanonicalFormAccepted(t *testing.T) {
	stipulate.Covers(t, "REQ-policy-record-location")
	cases := []struct {
		name   string
		policy *stipulatorv1.TestPolicy
	}{
		{"empty policy", mkPolicy()},
		{"one invocation", mkPolicy(mkInvocation("race", durationpb.New(900e9), true))},
		{"ascending names", mkPolicy(
			mkInvocation("member-race", durationpb.New(600e9), true),
			mkInvocation("workspace-race", durationpb.New(900e9), true),
		)},
		{"sub-second timeout", mkPolicy(mkInvocation("fast", durationpb.New(5e8), true))},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := Validate(c.policy); err != nil {
				t.Fatalf("canonical policy refused: %v", err)
			}
		})
	}
}

//gofresh:pure
func TestPolicyNonCanonicalFormRefusedWhole(t *testing.T) {
	stipulate.Covers(t, "REQ-policy-record-location")
	cases := []struct {
		name    string
		policy  *stipulatorv1.TestPolicy
		wantErr string
	}{
		{"empty invocation name",
			mkPolicy(mkInvocation("", durationpb.New(1e9), true)),
			"empty name"},
		{"duplicate names",
			mkPolicy(
				mkInvocation("race", durationpb.New(1e9), true),
				mkInvocation("race", durationpb.New(1e9), true),
			),
			"duplicate name"},
		{"descending order",
			mkPolicy(
				mkInvocation("workspace-race", durationpb.New(1e9), true),
				mkInvocation("member-race", durationpb.New(1e9), true),
			),
			"canonical order"},
		{"missing timeout",
			mkPolicy(mkInvocation("race", nil, true)),
			"missing explicit timeout"},
		{"zero timeout",
			mkPolicy(mkInvocation("race", durationpb.New(0), true)),
			"timeout must be positive"},
		{"invalid timeout overflow nanos",
			mkPolicy(mkInvocation("race", &durationpb.Duration{Seconds: 1, Nanos: 1_500_000_000}, true)),
			"invalid timeout"},
		{"invalid timeout beyond range",
			mkPolicy(mkInvocation("race", &durationpb.Duration{Seconds: 999_999_999_999_999}, true)),
			"invalid timeout"},
		{"negative timeout",
			mkPolicy(mkInvocation("race", durationpb.New(-1e9), true)),
			"timeout must be positive"},
		{"missing backend payload",
			mkPolicy(mkInvocation("race", durationpb.New(1e9), false)),
			"missing typed backend payload"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := Validate(c.policy)
			if err == nil {
				t.Fatal("non-canonical policy accepted")
			}
			if !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("error = %q, want it to name %q", err, c.wantErr)
			}
		})
	}
}

//gofresh:pure
func TestPolicyRecordParseRefusesMalformedWhole(t *testing.T) {
	stipulate.Covers(t, "REQ-policy-record-location")
	valid := `invocations {
  name: "workspace-race"
  timeout { seconds: 900 }
  go { packages: "./..." race: true }
}
`
	if p, err := Parse([]byte(valid)); err != nil {
		t.Fatalf("valid record refused: %v", err)
	} else if len(p.GetInvocations()) != 1 {
		t.Fatalf("invocations = %d, want 1", len(p.GetInvocations()))
	}

	cases := []struct {
		name, raw string
	}{
		{"syntax error", `invocations { name: `},
		{"unknown field", `invocations { name: "a" timeout { seconds: 1 } go {} flaky: true }`},
		{"duplicate scalar field", `invocations { name: "a" name: "b" timeout { seconds: 1 } go {} }`},
		{"non-canonical order", `invocations { name: "b" timeout { seconds: 1 } go {} }
invocations { name: "a" timeout { seconds: 1 } go {} }`},
		{"missing timeout", `invocations { name: "a" go {} }`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, err := Parse([]byte(c.raw))
			if err == nil {
				t.Fatal("malformed record accepted")
			}
			if p != nil {
				t.Fatal("refusal returned a partial policy")
			}
		})
	}
}
