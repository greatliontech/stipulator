// Package depbroken references a symbol its pinned dependency does not
// provide, so the package fails to load on a dependency-resolution
// state rather than an in-tree defect.
package depbroken

import "example.com/dep"

// Use references the absent export.
func Use() { dep.Missing() }
