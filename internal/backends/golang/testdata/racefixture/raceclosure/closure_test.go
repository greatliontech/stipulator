package raceclosure

import (
	"testing"
)

func TestRaceClosure(t *testing.T) {
	value := selectedValue()
	if value != "race-v1" && value != "race-v2" {
		t.Fatalf("race-selected value = %q", value)
	}
}
