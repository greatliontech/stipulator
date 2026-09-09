package coverage

import (
	"slices"
	"strings"
	"testing"

	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/stipulate"
)

// An enforcement pointer resolves to a tests- or proves-role binding of
// its own requirement whose member name it is; a pointer bound under
// another requirement, or under the implements role, or not at all,
// leaves the requirement red in the pointer's own class, counted apart,
// with the remedy named (REQ-change-enforcement-pointers).
//
//gofresh:pure
func TestEnforcementPointersAreJudgedAgainstTheRequirementsBindings(t *testing.T) {
	stipulate.Covers(t, "REQ-change-enforcement-pointers")
	doc := "# T\n\n**REQ-v-p** (behavior): It MUST x. Enforced by `TestP`, `TestQ`, and `TestR`.\n\n**REQ-v-o** (behavior): It MUST y.\n"
	spec, store := fixture(t, doc, map[string]string{
		".stipulator/bindings/m.textproto": "" +
			"bindings { requirement_id: \"REQ-v-p\" backend: \"go\" symbol: \"example.com/p.TestP\" role: BINDING_ROLE_TESTS }\n" +
			"bindings { requirement_id: \"REQ-v-o\" backend: \"go\" symbol: \"example.com/p.TestQ\" role: BINDING_ROLE_TESTS }\n" +
			"bindings { requirement_id: \"REQ-v-p\" backend: \"go\" symbol: \"example.com/p.TestR\" role: BINDING_ROLE_IMPLEMENTS }\n",
	})
	rep := Evaluate(spec, &verify.Report{}, store, true, nil)
	row := bucketOf(t, rep, "REQ-v-p")
	if row.Bucket != Broken {
		t.Fatalf("bucket = %v, want broken", row.Bucket)
	}
	joined := strings.Join(row.Reasons, "\n")
	for _, want := range []string{"enforcement pointer `TestQ`", "enforcement pointer `TestR`", "bind --req REQ-v-p --role tests --symbol <package>.TestQ", "or retarget the renamed symbol"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("reasons %q lack %q", joined, want)
		}
	}
	if strings.Contains(joined, "`TestP`") {
		t.Fatalf("the resolved pointer was reported: %q", joined)
	}
	if len(rep.DanglingPointers) != 2 || rep.DanglingPointers[0] != (DanglingPointer{"REQ-v-p", "TestQ"}) || rep.DanglingPointers[1] != (DanglingPointer{"REQ-v-p", "TestR"}) {
		t.Fatalf("dangling = %v", rep.DanglingPointers)
	}
	if n := DanglingPointerCount(rep.DanglingPointers, map[string]bool{"REQ-v-o": true}); n != 0 {
		t.Fatalf("scoped count = %d, want 0", n)
	}
	if n := DanglingPointerCount(rep.DanglingPointers, nil); n != 2 {
		t.Fatalf("count = %d, want 2", n)
	}
	if wire := rep.Proto().GetDanglingPointers(); len(wire) != 2 || wire[1].GetName() != "TestR" || wire[1].GetRequirementId() != "REQ-v-p" {
		t.Fatalf("wire rows = %v", wire)
	}
	// No gap excuses a dangling pointer: a gap declaring the broken
	// class on the requirement leaves it a violation, and says so.
	spec, store = fixture(t, doc, map[string]string{
		".stipulator/bindings/m.textproto": "bindings { requirement_id: \"REQ-v-p\" backend: \"go\" symbol: \"example.com/p.TestP\" role: BINDING_ROLE_TESTS }\n",
		".stipulator/gaps/p.textproto":     "requirement_id: \"REQ-v-p\" reason: \"later\" excuses: GAP_EXCUSE_BROKEN excuses: GAP_EXCUSE_UNCOVERED\n",
	})
	rep = Evaluate(spec, &verify.Report{}, store, true, nil)
	if !slices.Contains(rep.Violations, "REQ-v-p") {
		t.Fatalf("a gap excused a dangling pointer: violations = %v", rep.Violations)
	}
	if reasons := strings.Join(bucketOf(t, rep, "REQ-v-p").Reasons, "\n"); !strings.Contains(reasons, "excuses nothing about its dangling enforcement pointer") {
		t.Fatalf("the gap's impotence is unstated: %q", reasons)
	}
	// A proves-role binding resolves too.
	spec, store = fixture(t, doc, map[string]string{
		".stipulator/bindings/m.textproto": "" +
			"bindings { requirement_id: \"REQ-v-p\" backend: \"go\" symbol: \"example.com/p.TestP\" role: BINDING_ROLE_TESTS }\n" +
			"bindings { requirement_id: \"REQ-v-p\" backend: \"go\" symbol: \"example.com/p.TestQ\" role: BINDING_ROLE_PROVES }\n" +
			"bindings { requirement_id: \"REQ-v-p\" backend: \"go\" symbol: \"example.com/q.TestR\" role: BINDING_ROLE_TESTS }\n",
	})
	if rep := Evaluate(spec, &verify.Report{}, store, true, nil); len(rep.DanglingPointers) != 0 {
		t.Fatalf("every pointer bound, yet dangling = %v", rep.DanglingPointers)
	}
}
