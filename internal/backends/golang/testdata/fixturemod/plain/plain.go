// Package plain is a rapid-free fixture: its passing test witnesses
// symbols in other packages, making their witness unions span rapid and
// non-rapid test binaries.
package plain

// Ok reports fixture health.
func Ok() bool { return true }
