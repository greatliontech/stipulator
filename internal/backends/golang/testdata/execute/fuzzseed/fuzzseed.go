// Package fuzzseed carries a fuzz target whose committed seed fails
// deterministic replay.
package fuzzseed

// Refuses reports whether s is the refused value.
func Refuses(s string) bool { return s == "bad" }
