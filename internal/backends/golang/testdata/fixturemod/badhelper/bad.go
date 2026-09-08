// Package badhelper type-checks with an error: its helper calls an
// undeclared function, so a walk through it resolves that call to no
// declaration.
package badhelper

import (
	"testing"

	"pgregory.net/rapid"
)

// Run reaches nothing the walk can read.
func Run(t *testing.T, body func(*rapid.T)) {
	mystery(t)
}
