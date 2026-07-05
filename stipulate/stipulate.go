// Package stipulate is the runtime coverage-registration helper for Go
// tests.
//
// A test calls Covers to claim, at runtime, that it exercises a
// requirement. The registration rides the test's own log output, so it is
// witnessed only when the test actually ran — and it is a claim like any
// other: the verifier cross-checks every registration against the binding
// store and rejects registrations no binding backs.
package stipulate

import "testing"

// Marker prefixes a coverage registration in test output. It is the wire
// contract between this helper and the witness correlator. Registrations
// should be emitted through Covers: t.Log output is reliably attributed to
// the running test, while raw prints from stray goroutines can attach to
// the wrong test.
const Marker = "stipulator:covers "

// Covers registers that the running test (or subtest) exercises the given
// requirements.
func Covers(t testing.TB, ids ...string) {
	t.Helper()
	for _, id := range ids {
		t.Log(Marker + id)
	}
}
