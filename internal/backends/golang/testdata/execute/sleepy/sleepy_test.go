package sleepy

import (
	"testing"
	"time"
)

// TestSleeps outlasts a small envelope or a small go-test-level timeout;
// it passes under a generous one.
func TestSleeps(t *testing.T) {
	time.Sleep(2 * time.Second)
}
