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

// Add exists for mutation testing: TestAdd pins both branches, so every
// mutant dies.
func Add(a, b int) int {
	if a == 0 {
		return b
	}
	return a + b
}

// Weak exists for mutation testing: TestWeak never exercises the large-x
// branch, so mutants inside it survive.
func Weak(x int) int {
	if x > 100 {
		return x - 1
	}
	return x
}

// Mixed exists for operator coverage: assignments, compound arithmetic,
// logical operands, loops, and literals — one site per operator family.
func Mixed(xs []int) int {
	total := 0
	for _, x := range xs {
		if x < 0 || x > 99 {
			continue
		}
		total += x * 2
	}
	total = total + 3
	return total
}
