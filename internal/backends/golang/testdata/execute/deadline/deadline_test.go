package deadline

import (
	"testing"
	"time"
)

// TestQuick completes well inside any reviewed binary bound, so its
// completed pass exists inside the deadline-cut package process.
func TestQuick(t *testing.T) {}

// TestStall outlasts a small -test.timeout, so a deadline panic cuts it
// off both in the package process and in its own solo re-run.
func TestStall(t *testing.T) {
	time.Sleep(3 * time.Second)
}
