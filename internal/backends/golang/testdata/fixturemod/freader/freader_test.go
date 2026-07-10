// Package freader pins the purity + runtime-input path of the witness
// cache: the test reads a module-relative fixture, asserts purity in
// source (the deliberate opt-in), and so serves from cache exactly until
// the fixture's content moves.
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
