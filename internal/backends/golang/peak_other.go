//go:build !linux

package golang

import "os"

// processPeakBytes answers nothing: the reaped process's maxrss has a
// Linux kibibyte form only, and the admission derives no completed-package
// evidence elsewhere.
func processPeakBytes(*os.ProcessState) uint64 { return 0 }
