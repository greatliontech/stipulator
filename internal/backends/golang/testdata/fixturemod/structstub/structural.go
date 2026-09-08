// Package structural is a hermetic stub of the analyzer library: the
// classifier resolves the import path and assertion names from the type
// checker, never the assertions themselves.
package structural

import "testing"

// Implements mirrors the interface-satisfaction assertion.
func Implements[T any](t testing.TB, v any) {}
