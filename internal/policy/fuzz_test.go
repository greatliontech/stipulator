package policy

import (
	"testing"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
)

// FuzzPolicyTextprotoParse quantifies over policy record bytes: a record
// either parses into a canonical policy that survives a text round trip,
// or is refused whole — no input yields a partial or non-canonical policy.
func FuzzPolicyTextprotoParse(f *testing.F) {
	for _, seed := range []string{
		"",
		"invocations { name: \"a\" timeout { seconds: 1 } go { packages: \"./...\" race: true } }",
		"invocations { name: \"a\" timeout { seconds: 1 } go {} }\ninvocations { name: \"b\" timeout { seconds: 900 nanos: 1 } go { module_root: \"m\" } }",
		// Malformed: syntax, unknown field, duplicate scalar, duplicate
		// oneof case, out of order, missing envelope facts.
		"invocations { name:",
		"invocations { name: \"a\" timeout { seconds: 1 } go {} flaky: true }",
		"invocations { name: \"a\" name: \"b\" timeout { seconds: 1 } go {} }",
		"invocations { name: \"a\" timeout { seconds: 1 nanos: 1500000000 } go {} }",
		"invocations { name: \"a\" timeout { seconds: 999999999999999 } go {} }",
		"invocations { name: \"a\" timeout { seconds: 2 nanos: -1 } go {} }",
		"invocations { name: \"a\" timeout { seconds: 1 } go {} go {} }",
		"invocations { name: \"b\" timeout { seconds: 1 } go {} }\ninvocations { name: \"a\" timeout { seconds: 1 } go {} }",
		"invocations { name: \"a\" go {} }",
		"invocations { name: \"a\" timeout { seconds: -1 } go {} }",
		"invocations { timeout { seconds: 1 } go {} }",
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		p, err := Parse(raw)
		if err != nil {
			if p != nil {
				t.Fatal("refusal returned a partial policy")
			}
			return
		}
		// Accepted implies canonical: names strictly ascending, every
		// envelope complete.
		prev := ""
		for i, inv := range p.GetInvocations() {
			if inv.GetName() == "" || (i > 0 && inv.GetName() <= prev) {
				t.Fatalf("accepted policy is non-canonical at %d: %q after %q", i, inv.GetName(), prev)
			}
			prev = inv.GetName()
			if inv.GetTimeout() == nil || inv.WhichConfig() == stipulatorv1.PolicyInvocation_Config_not_set_case {
				t.Fatalf("accepted invocation %q has an incomplete envelope", inv.GetName())
			}
			if inv.GetTimeout().CheckValid() != nil || inv.GetTimeout().AsDuration() <= 0 {
				t.Fatalf("accepted invocation %q has an invalid or non-positive timeout", inv.GetName())
			}
		}
		// Text round trip preserves the message and stays canonical.
		out, err := prototext.Marshal(p)
		if err != nil {
			t.Fatalf("marshal of accepted policy: %v", err)
		}
		again, err := Parse(out)
		if err != nil {
			t.Fatalf("round trip refused: %v\n%s", err, out)
		}
		if !proto.Equal(p, again) {
			t.Fatalf("round trip changed the policy:\n%v\n%v", p, again)
		}
		// The owned renderer is part of the record surface: whatever
		// parses canonically renders deterministically and strict-parses
		// back whole.
		rendered, err := Render(p)
		if err != nil {
			t.Fatalf("render of accepted policy: %v", err)
		}
		fromRender, err := Parse(rendered)
		if err != nil {
			t.Fatalf("rendered record refused: %v\n%s", err, rendered)
		}
		if !proto.Equal(p, fromRender) {
			t.Fatalf("render round trip changed the policy:\n%s", rendered)
		}
	})
}

// FuzzPolicyProtoJSON quantifies over ProtoJSON policy bytes — unknown
// fields, duplicate keys, nulls, wrong types among the seeds — checking
// that whatever unmarshals survives a canonical JSON round trip.
func FuzzPolicyProtoJSON(f *testing.F) {
	for _, seed := range []string{
		"{}",
		`{"invocations":[{"name":"a","timeout":"900s","go":{"packages":["./..."],"race":true}}]}`,
		`{"bogus":1}`,
		`{"invocations":[{"name":"a","name":"b"}]}`,
		`{"invocations":null}`,
		`{"invocations":[{"name":null}]}`,
		`{"invocations":[{"timeout":900}]}`,
		`{"invocations":{}}`,
		"null",
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		p := &stipulatorv1.TestPolicy{}
		if err := protojson.Unmarshal(raw, p); err != nil {
			return
		}
		roundTripCanonicalJSON(t, p, &stipulatorv1.TestPolicy{})
	})
}

// FuzzCheckResultProtoJSON does the same over the unified check result,
// the message CLI and MCP consumers read as JSON.
func FuzzCheckResultProtoJSON(f *testing.F) {
	for _, seed := range []string{
		"{}",
		`{"passed":true,"execution":{"invocations":[{"invocation":"a","disposition":"HEALTH_DISPOSITION_HEALTHY"}]}}`,
		`{"passed":"yes"}`,
		`{"execution":null}`,
		`{"pruneResidue":[null]}`,
		`{"passed":true,"passed":false}`,
		`{"compileProblems":[{"path":"x","unknown":1}]}`,
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		m := &stipulatorv1.CheckResult{}
		if err := protojson.Unmarshal(raw, m); err != nil {
			return
		}
		roundTripCanonicalJSON(t, m, &stipulatorv1.CheckResult{})
	})
}

// roundTripCanonicalJSON asserts the canonical JSON projection of m
// unmarshals back to an equal message.
func roundTripCanonicalJSON(t *testing.T, m, fresh proto.Message) {
	t.Helper()
	b, err := canonicalJSON(m)
	if err != nil {
		t.Fatalf("canonical JSON: %v", err)
	}
	if err := protojson.Unmarshal(b, fresh); err != nil {
		t.Fatalf("canonical JSON does not unmarshal: %v\n%s", err, b)
	}
	if !proto.Equal(m, fresh) {
		t.Fatalf("JSON round trip changed the message:\n%v\n%v", m, fresh)
	}
}
