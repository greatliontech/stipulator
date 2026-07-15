// Package lib is hand-written fixture code.
package lib

import "example.com/fixture/genp"

// F is a plain function.
func F() {}

// W embeds a generated type; W.M is promoted from generated code.
type W struct {
	genp.G
}

// I is an interface; its methods resolve via the value method set.
type I interface {
	M(x int) error
}

// Add exercises a function with two branches.
func Add(a, b int) int {
	if a == 0 {
		return b
	}
	return a + b
}
