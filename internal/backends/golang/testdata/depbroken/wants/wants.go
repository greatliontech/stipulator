// Package wants references a symbol a sibling package of its own
// module does not declare — an in-tree defect, not a dependency state.
package wants

import "example.com/depbroken/has"

// W references the absent in-tree export.
func W() { has.Gone() }
