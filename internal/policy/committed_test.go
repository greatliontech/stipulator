package policy

import (
	"os"
	"path/filepath"
	"testing"
)

// The committed policy is live configuration: an unparseable file
// leaves gate, check, and verify inoperable on the tree, and nothing
// else in the suite reads it.
//
//gofresh:pure
func TestCommittedPolicyParses(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", Path))
	if err != nil {
		t.Fatalf("committed policy unreadable: %v", err)
	}
	if _, err := Parse(raw); err != nil {
		t.Fatalf("committed policy refuses to parse: %v", err)
	}
}
