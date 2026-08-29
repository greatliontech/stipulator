package author

import (
	"errors"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/greatliontech/stipulator/internal/records"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/stipulate"
)

// recordingBackend resolves from a fixed shape map, errors on a named
// symbol, and records every resolution it was asked for.
type recordingBackend struct {
	shapes map[string]string
	faulty string
	asked  []string
}

func (b *recordingBackend) Resolve(symbol string) (verify.Resolution, string, error) {
	b.asked = append(b.asked, symbol)
	if symbol == b.faulty {
		return verify.NotFound, "", errors.New("resolver crashed")
	}
	if s, ok := b.shapes[symbol]; ok {
		return verify.Resolved, s, nil
	}
	return verify.NotFound, "", nil
}

// ResolveShapes' two load-bearing behaviors, pinned where they live: a
// per-symbol resolution fault reaches the caller's onFault instead of
// silently emptying the shape map, and the wanted filter scopes
// resolution to the named requirements' bindings — an unnamed
// requirement's symbol is never resolved (REQ-pin-backfill).
//
//gofresh:pure
func TestResolveShapesSurfacesFaultsAndScopesToWanted(t *testing.T) {
	stipulate.Covers(t, "REQ-pin-backfill")
	shape := strings.Repeat("b", 64)
	store, err := records.Load(fstest.MapFS{
		".stipulator/bindings/t.textproto": {Data: []byte(
			"bindings {\n  requirement_id: \"REQ-rs-a\"\n  backend: \"go\"\n  symbol: \"example.com/p.Good\"\n  role: BINDING_ROLE_IMPLEMENTS\n}\n\n" +
				"bindings {\n  requirement_id: \"REQ-rs-a\"\n  backend: \"go\"\n  symbol: \"example.com/p.Bad\"\n  role: BINDING_ROLE_TESTS\n}\n\n" +
				"bindings {\n  requirement_id: \"REQ-rs-z\"\n  backend: \"go\"\n  symbol: \"example.com/p.Other\"\n  role: BINDING_ROLE_IMPLEMENTS\n}\n")},
	})
	if err != nil {
		t.Fatal(err)
	}
	backend := &recordingBackend{shapes: map[string]string{"example.com/p.Good": shape}, faulty: "example.com/p.Bad"}
	var faults []string
	shapes := ResolveShapes(store, map[string]verify.Backend{"go": backend}, map[string]bool{"REQ-rs-a": true}, func(symbol string, err error) {
		faults = append(faults, symbol+": "+err.Error())
	})
	if len(shapes) != 1 || shapes[records.ShapeKey("go", "example.com/p.Good")] != shape {
		t.Fatalf("shapes = %v, want the one resolvable wanted symbol", shapes)
	}
	if len(faults) != 1 || !strings.Contains(faults[0], "example.com/p.Bad: resolver crashed") {
		t.Fatalf("faults = %v; a swallowed resolution fault turns a mismatch answer back into a quiescence claim", faults)
	}
	for _, sym := range backend.asked {
		if sym == "example.com/p.Other" {
			t.Fatal("an unnamed requirement's symbol was resolved — the wanted filter is not scoping")
		}
	}
	if len(backend.asked) != 2 {
		t.Fatalf("asked = %v, want exactly the wanted requirement's two symbols", backend.asked)
	}
}
