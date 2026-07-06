package records

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/greatliontech/stipulator/stipulate"
)

// storeWith loads a store holding one binding file with the given text.
func storeWith(t *testing.T, bindings string) *Store {
	t.Helper()
	store, err := Load(fstest.MapFS{
		".stipulator/bindings/t.textproto": {Data: []byte(bindings)},
	})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

// TestPinNeverRewritesDifferingContent pins REQ-pin-backfill's core rule:
// the blanket form sets only UNSET content pins — a differing pin is the
// consent trail, rewritten only through the named editorial path, so
// staleness cannot be laundered.
func TestPinNeverRewritesDifferingContent(t *testing.T) {
	stipulate.Covers(t, "REQ-pin-backfill")
	stale := strings.Repeat("0", 64)
	current := strings.Repeat("1", 64)
	store := storeWith(t, "bindings {\n  requirement_id: \"REQ-r-a\"\n  backend: \"go\"\n  symbol: \"example.com/p.F\"\n  role: BINDING_ROLE_IMPLEMENTS\n  content_hash: \""+stale+"\"\n}\n")
	ups, err := Pin(store, map[string]string{"REQ-r-a": current}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(ups) != 0 {
		t.Fatalf("differing content pin rewritten by the blanket form: %v", ups)
	}
}
