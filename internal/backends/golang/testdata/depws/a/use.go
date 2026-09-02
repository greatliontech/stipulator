// Package a references a symbol its sibling workspace member does not
// declare — attributable to the member pin: the working copy itself
// lacks the surface.
package a

import "example.com/depws/b"

// Use references the absent member export.
func Use() { b.Gone() }
