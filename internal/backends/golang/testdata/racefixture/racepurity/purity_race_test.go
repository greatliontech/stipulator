//go:build race

package racepurity

import (
	"os"
	"testing"
)

func TestRacePurity(t *testing.T) {
	if _, err := os.ReadFile("fixture.txt"); err != nil {
		t.Fatal(err)
	}
}
