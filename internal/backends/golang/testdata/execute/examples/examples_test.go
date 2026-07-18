package examples

import "fmt"

func Example_pass() {
	fmt.Println("expected output")
	// Output: expected output
}

func Example_fail() {
	fmt.Println("actual output")
	// Output: something else entirely
}
