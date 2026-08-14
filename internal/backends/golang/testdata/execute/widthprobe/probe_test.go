package widthprobe

import (
	"os"
	"testing"
)

// TestWidthDelivered exists for spawn-environment testing: when the
// spawning harness arms it, it fails exactly when the harness delivered
// an inner-parallelism cap other than the armed value (a missing cap
// included), so a harness test can tell a capped spawn from an uncapped
// one in either direction. Unarmed selections skip it, so incidental
// ./... sweeps are unaffected.
func TestWidthDelivered(t *testing.T) {
	want := os.Getenv("STIPULATOR_FIXTURE_REQUIRE_WIDTH")
	if want == "" {
		t.Skip("width assertion not requested")
	}
	if got := os.Getenv("GOMAXPROCS"); got != want {
		t.Fatalf("GOMAXPROCS = %q, want %q", got, want)
	}
}
