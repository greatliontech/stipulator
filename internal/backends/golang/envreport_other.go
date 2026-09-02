//go:build !unix

package golang

// processLimits has no portable non-unix rendering; the report simply
// omits the limits line.
func processLimits() string { return "" }
