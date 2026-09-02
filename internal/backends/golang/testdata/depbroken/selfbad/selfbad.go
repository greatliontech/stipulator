// Package selfbad carries an in-tree defect with no dependency root,
// pinning that attribution never fires on unrecognized shapes.
package selfbad

// F calls a name this package never declares.
func F() { undeclaredIdent() }
