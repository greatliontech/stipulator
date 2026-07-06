package author

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/greatliontech/stipulator/stipulate"
)

const sheet = `records {
  backend: "go"
  symbol: "example.com/p.F"
  body_hash: "` + "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" + `"
  mutants: 3
  killed: 2
  survivors {
    position: "f.go:10:2"
    operator: "drop assignment"
  }
  witnesses: "example.com/p.TestF"
  operators: "go/2"
}
`

// TestAttest pins the attestation contract: only a present survivor can
// be attested, with reasoning, once; the disposition lands on the sheet.
func TestAttest(t *testing.T) {
	stipulate.Covers(t, "REQ-harden-attestation")
	fsys := fstest.MapFS{
		".stipulator/hardening/f.textproto": {Data: []byte(sheet)},
	}

	up, err := Attest(fsys, "example.com/p.F", "f.go:10:2", "drop assignment", "the store is re-derived two lines later")
	if err != nil {
		t.Fatal(err)
	}
	if up.Path != ".stipulator/hardening/f.textproto" {
		t.Fatalf("attestation wrote elsewhere: %s", up.Path)
	}
	for _, want := range []string{"attested {", `position: "f.go:10:2"`, "re-derived two lines later"} {
		if !strings.Contains(string(up.Content), want) {
			t.Fatalf("attestation missing %q:\n%s", want, up.Content)
		}
	}

	// A mutant that did not survive cannot be attested.
	if _, err := Attest(fsys, "example.com/p.F", "f.go:99:1", "drop assignment", "r"); err == nil || !strings.Contains(err.Error(), "no survivor") {
		t.Fatalf("non-survivor attested: %v", err)
	}
	// Reasoning is mandatory.
	if _, err := Attest(fsys, "example.com/p.F", "f.go:10:2", "drop assignment", ""); err == nil || !strings.Contains(err.Error(), "reason") {
		t.Fatalf("reasonless attestation accepted: %v", err)
	}
	// A second attestation of the same survivor is refused.
	fsys[".stipulator/hardening/f.textproto"] = &fstest.MapFile{Data: up.Content}
	if _, err := Attest(fsys, "example.com/p.F", "f.go:10:2", "drop assignment", "again"); err == nil || !strings.Contains(err.Error(), "already attested") {
		t.Fatalf("duplicate attestation accepted: %v", err)
	}
	// No sheet, no attestation.
	if _, err := Attest(fsys, "example.com/p.Ghost", "f.go:10:2", "drop assignment", "r"); err == nil || !strings.Contains(err.Error(), "no kill-sheet") {
		t.Fatalf("sheetless attestation accepted: %v", err)
	}
}

// TestAttestRequirement pins the evidence-attestation verb: reasoned,
// corpus-validated, content-pinned at write, one per requirement.
func TestAttestRequirement(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-attestation")
	fsys := testFS(nil)

	up, err := AttestRequirement(fsys, "REQ-au-a", "review judged this satisfied")
	if err != nil {
		t.Fatal(err)
	}
	if up.Path != ".stipulator/attestations/au-a.textproto" {
		t.Fatalf("path = %s", up.Path)
	}
	for _, want := range []string{`requirement_id: "REQ-au-a"`, "review judged this satisfied", "content_hash: "} {
		if !strings.Contains(string(up.Content), want) {
			t.Fatalf("attestation missing %q:\n%s", want, up.Content)
		}
	}

	if _, err := AttestRequirement(fsys, "REQ-au-a", ""); err == nil || !strings.Contains(err.Error(), "reason") {
		t.Fatalf("reasonless attestation accepted: %v", err)
	}
	if _, err := AttestRequirement(fsys, "REQ-au-ghost", "r"); err == nil || !strings.Contains(err.Error(), "not in the corpus") {
		t.Fatalf("ghost requirement attested: %v", err)
	}
	fsys[up.Path] = &fstest.MapFile{Data: up.Content}
	if _, err := AttestRequirement(fsys, "REQ-au-a", "again"); err == nil || !strings.Contains(err.Error(), "already attested") {
		t.Fatalf("duplicate attestation accepted: %v", err)
	}
}
