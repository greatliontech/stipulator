// Package rapid is a hermetic stub of pgregory.net/rapid: the classifier
// resolves the import path and driver names from the type checker, so the
// fixture needs the shapes, not the behavior.
package rapid

import (
	"flag"
	"testing"
)

// The real library registers its flags in package init; test binaries
// linking this stub must accept them too, or every harden run against a
// rapid-importing fixture package would die on an unknown flag and read
// as a false kill.
func init() {
	flag.Bool("rapid.nofailfile", false, "rapid: do not write fail files on test failures")
}

// T mirrors rapid.T as the property callback's handle.
type T struct{ testing.TB }

// Check mirrors the check driver: it runs the property once.
func Check(t *testing.T, prop func(*T)) { prop(&T{TB: t}) }

// MakeCheck mirrors the subtest-shaped driver.
func MakeCheck(prop func(*T)) func(*testing.T) {
	return func(t *testing.T) { prop(&T{TB: t}) }
}

// Int mirrors a generator constructor: construction alone must not
// classify as a property witness.
func Int() int { return 0 }
