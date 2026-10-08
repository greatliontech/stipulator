//go:build race

package racepurity

import (
	"os"
	"testing"
)

func TestRacePurity(t *testing.T) {
	if _, ok := os.LookupEnv("HOME"); !ok {
		t.Fatal("missing observed environment")
	}
}
