package author

import (
	"slices"
	"testing"
	"testing/fstest"

	"github.com/greatliontech/stipulator/internal/records"
)

func TestPinSymbolsSelectsCompleteNamedSet(t *testing.T) {
	store, err := records.Load(fstest.MapFS{
		".stipulator/bindings/a.textproto": {Data: []byte(`
bindings { requirement_id: "REQ-a" backend: "go" symbol: "example.com/p.A" role: BINDING_ROLE_IMPLEMENTS }
bindings { requirement_id: "REQ-b" backend: "go" symbol: "example.com/q.B" role: BINDING_ROLE_TESTS }
bindings { requirement_id: "REQ-a" backend: "go" symbol: "example.com/p.A" role: BINDING_ROLE_TESTS }
bindings { requirement_id: "REQ-c" backend: "go" symbol: "example.com/bad.C" role: BINDING_ROLE_TESTS }
bindings { requirement_id: "REQ-b" backend: "proto" symbol: "foreign.Message" role: BINDING_ROLE_IMPLEMENTS }
`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := PinSymbols(store, "go", map[string]bool{"REQ-a": true, "REQ-b": true}); !slices.Equal(got, []string{"example.com/p.A", "example.com/q.B"}) {
		t.Fatalf("selected set %v", got)
	}
	for _, wanted := range []map[string]bool{nil, {}, {"REQ-c": false}, {"REQ-absent": true}} {
		if got := PinSymbols(store, "go", wanted); got == nil || len(got) != 0 {
			t.Fatalf("explicit empty selection %v became %v", wanted, got)
		}
	}
}
