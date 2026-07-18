package policy

import (
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/greatliontech/stipulator/stipulate"
)

// TestPolicyRenderRoundTripsCanonically pins the owned renderer: fixed
// bytes for a given policy (prototext.Marshal deliberately destabilizes
// whitespace, a committed record cannot), and a strict-parse round trip
// back to the same message.
//
//gofresh:pure
func TestPolicyRenderRoundTripsCanonically(t *testing.T) {
	stipulate.Covers(t, "REQ-policy-record-location")
	p := mkPolicy(
		mkGoInvocation("member-race", "member/nested", 10*time.Minute),
		mkGoInvocation("workspace-race", "", 15*time.Minute),
	)
	rendered, err := Render(p)
	if err != nil {
		t.Fatal(err)
	}
	want := "# proto-file: proto/stipulator/v1/policy.proto\n" +
		"# proto-message: stipulator.v1.TestPolicy\n" +
		"\n" +
		"invocations {\n" +
		"  name: \"member-race\"\n" +
		"  timeout {\n" +
		"    seconds: 600\n" +
		"  }\n" +
		"  go {\n" +
		"    module_root: \"member/nested\"\n" +
		"    packages: \"./...\"\n" +
		"    race: true\n" +
		"  }\n" +
		"}\n" +
		"invocations {\n" +
		"  name: \"workspace-race\"\n" +
		"  timeout {\n" +
		"    seconds: 900\n" +
		"  }\n" +
		"  go {\n" +
		"    packages: \"./...\"\n" +
		"    race: true\n" +
		"  }\n" +
		"}\n"
	if string(rendered) != want {
		t.Errorf("rendered record drifted:\n%s", rendered)
	}
	again, err := Render(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != string(rendered) {
		t.Error("rendering is not deterministic")
	}
	parsed, err := Parse(rendered)
	if err != nil {
		t.Fatalf("rendered record does not strict-parse: %v", err)
	}
	if !proto.Equal(parsed, p) {
		t.Error("rendered record parses back to a different policy")
	}

	empty, err := Render(mkPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if p2, err := Parse(empty); err != nil {
		t.Fatalf("empty record does not strict-parse: %v", err)
	} else if len(p2.GetInvocations()) != 0 {
		t.Error("empty policy round-trips invocations")
	}
}
