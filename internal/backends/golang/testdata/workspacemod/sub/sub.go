// Package sub is a nested workspace member: a published module whose
// symbols and witnesses must stay inside verification.
package sub

// Nested is resolvable only if the engine walks workspace members.
func Nested() int { return 2 }
