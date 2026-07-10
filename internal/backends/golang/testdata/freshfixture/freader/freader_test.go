package freader

import (
	"os"
	"testing"
)

//gofresh:pure
func TestReadsFixture(t *testing.T) {
	b, err := os.ReadFile("data.txt")
	if err != nil {
		t.Fatal(err)
	}
	if len(b) == 0 {
		t.Fatal("empty fixture")
	}
}
