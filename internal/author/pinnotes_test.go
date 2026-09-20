package author

import (
	"testing"

	"github.com/greatliontech/stipulator/stipulate"
)

// The pin notes both faces print are composed from the one remedy
// spelling: the blanket re-pin for a moved shape, the ids-form re-consent
// for the requirements a blanket pin left awaiting it — as literals,
// never read from the composers under test.
func TestPinNotesNameTheirRemedies(t *testing.T) {
	stipulate.Covers(t, "REQ-change-remediation")
	if got := ShapeMovedNote([]string{"example.com/p.F", "example.com/p.G"}); got != "shape of example.com/p.F, example.com/p.G moved — the ids form re-consents clause text only: blanket stipulator pin re-pins shapes" {
		t.Fatalf("shape note = %q", got)
	}
	if got := AwaitingReconsent([]string{"REQ-a", "REQ-b"}); got != "awaiting re-consent — run stipulator pin --req REQ-a --req REQ-b" {
		t.Fatalf("awaiting note = %q", got)
	}
}
