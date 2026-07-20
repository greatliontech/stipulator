package author

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/greatliontech/stipulator/stipulate"
)

// attestableFS is the attest fixture: REQ-au-s sits in a SHOULD cell,
// which the default policy admits attestation for; the MUST cells
// refuse at write time (the born-valid check).
func attestableFS(files map[string]string) fstest.MapFS {
	fsys := testFS(files)
	fsys["specs/should.md"] = &fstest.MapFile{Data: []byte(
		"# S\n\n**REQ-au-s** (behavior): It SHOULD s.\n")}
	return fsys
}

// TestAttestRequirement pins the evidence-attestation verb: reasoned,
// corpus-validated, content-pinned at write, one per requirement.
//
//gofresh:pure
func TestAttestRequirement(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-attestation")
	fsys := attestableFS(nil)

	up, prior, err := AttestRequirement(fsys, "REQ-au-s", "review judged this satisfied")
	if err != nil {
		t.Fatal(err)
	}
	if up.Path != ".stipulator/attestations/au-s.textproto" {
		t.Fatalf("path = %s", up.Path)
	}
	if prior != nil {
		t.Fatalf("fresh attestation reported a prior: %v", prior)
	}
	for _, want := range []string{`requirement_id: "REQ-au-s"`, "review judged this satisfied", "content_hash: "} {
		if !strings.Contains(string(up.Content), want) {
			t.Fatalf("attestation missing %q:\n%s", want, up.Content)
		}
	}

	if _, _, err := AttestRequirement(fsys, "REQ-au-s", ""); err == nil || !strings.Contains(err.Error(), "reason") {
		t.Fatalf("reasonless attestation accepted: %v", err)
	}
	if _, _, err := AttestRequirement(fsys, "REQ-au-ghost", "r"); err == nil || !strings.Contains(err.Error(), "not in the corpus") {
		t.Fatalf("ghost requirement attested: %v", err)
	}
	// Re-judging replaces in place and surfaces the superseded reasoning.
	fsys[up.Path] = &fstest.MapFile{Data: up.Content}
	up2, prior2, err := AttestRequirement(fsys, "REQ-au-s", "re-judged after refactor")
	if err != nil {
		t.Fatal(err)
	}
	if up2.Path != up.Path || prior2.GetReason() != "review judged this satisfied" {
		t.Fatalf("replace: path=%s prior=%v", up2.Path, prior2)
	}
	if !strings.Contains(string(up2.Content), "re-judged after refactor") ||
		strings.Contains(string(up2.Content), "review judged this satisfied") {
		t.Fatalf("replacement accreted instead of replacing:\n%s", up2.Content)
	}

	// Retraction withdraws the judgment and deletes an emptied file.
	fsys[up2.Path] = &fstest.MapFile{Data: up2.Content}
	del, retracted, err := RetractAttestation(fsys, "REQ-au-s")
	if err != nil {
		t.Fatal(err)
	}
	if del.Path != up2.Path || del.Content != nil || retracted.GetReason() != "re-judged after refactor" {
		t.Fatalf("retract: %+v %v", del, retracted)
	}
	if _, _, err := RetractAttestation(testFS(nil), "REQ-au-s"); err == nil || !strings.Contains(err.Error(), "nothing to retract") {
		t.Fatalf("empty retract accepted: %v", err)
	}

	// Born-valid: a cell that can never render the attested bucket
	// refuses at write time, naming its real demand
	// (REQ-change-remediation).
	if _, _, err := AttestRequirement(fsys, "REQ-au-a", "judged"); err == nil ||
		!strings.Contains(err.Error(), "never admits attestation") ||
		!strings.Contains(err.Error(), "executed witness") {
		t.Fatalf("MUST-cell attestation error = %v, want the cell's demand", err)
	}
}

// TestAttestRequirementMultiRecordFile pins the two-pass replace: in a
// hand-edited multi-attestation file, records preceding AND following the
// judgment survive a replace untouched.
//
//gofresh:pure
func TestAttestRequirementMultiRecordFile(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-attestation")
	fsys := attestableFS(map[string]string{
		".stipulator/attestations/combo.textproto": "attestations {\n  requirement_id: \"REQ-au-b\"\n  reason: \"before\"\n}\nattestations {\n  requirement_id: \"REQ-au-s\"\n  reason: \"old\"\n}\n",
	})
	up, prior, err := AttestRequirement(fsys, "REQ-au-s", "new judgment")
	if err != nil {
		t.Fatal(err)
	}
	if up.Path != ".stipulator/attestations/combo.textproto" || prior.GetReason() != "old" {
		t.Fatalf("replace misplaced: %s prior=%v", up.Path, prior)
	}
	content := string(up.Content)
	if !strings.Contains(content, "\"before\"") || !strings.Contains(content, "new judgment") || strings.Contains(content, "\"old\"") {
		t.Fatalf("unrelated record lost or accretion:\n%s", content)
	}
}
