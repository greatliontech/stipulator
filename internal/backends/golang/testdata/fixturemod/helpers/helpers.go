// Package helpers wraps the property driver for tests in other packages
// of the fixture module.
package helpers

import (
	"testing"

	"pgregory.net/rapid"
)

// Run drives the property from another package.
func Run(t *testing.T, body func(*rapid.T)) {
	rapid.Check(t, body)
}
