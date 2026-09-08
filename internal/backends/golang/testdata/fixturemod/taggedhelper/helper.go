//go:build fixturetag

// Package taggedhelper exists only under a build tag no fixture view
// selects: a test importing it resolves its calls to no declaration.
package taggedhelper

import (
	"testing"

	"pgregory.net/rapid"
)

// Run drives the property.
func Run(t *testing.T, body func(*rapid.T)) { rapid.Check(t, body) }
