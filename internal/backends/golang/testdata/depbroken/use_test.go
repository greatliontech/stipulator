package depbroken

import "testing"

// TestUses is a bound witness whose package cannot load.
func TestUses(t *testing.T) { Use() }
