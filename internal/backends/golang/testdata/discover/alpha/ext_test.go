package alpha_test

import (
	"fmt"
	"testing"

	"example.com/disc/alpha"
)

func TestExternal(t *testing.T) {
	if alpha.Greet() == "" {
		t.Fatal("empty greeting")
	}
}

func ExampleGreet() {
	fmt.Println(alpha.Greet())
	// Output: hi
}

// ExampleGreet_silent has no output comment: compiled, never run.
func ExampleGreet_silent() {
	_ = alpha.Greet()
}
