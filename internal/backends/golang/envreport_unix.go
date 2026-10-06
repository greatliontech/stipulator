//go:build unix

package golang

import (
	"fmt"
	"strings"
	"syscall"
)

// processLimits renders the runner process's resource limits — the
// limits its witness children inherit. A read fault yields an empty
// report line rather than a failed diagnostic: limits are candidate
// variables, not evidence.
func processLimits() string {
	limits := []struct {
		name string
		res  int
	}{
		{"nofile", syscall.RLIMIT_NOFILE},
		{"stack", syscall.RLIMIT_STACK},
		{"cpu", syscall.RLIMIT_CPU},
	}
	var parts []string
	for _, l := range limits {
		var r syscall.Rlimit
		if err := syscall.Getrlimit(l.res, &r); err != nil {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=%s/%s", l.name, rlimitValue(r.Cur), rlimitValue(r.Max)))
	}
	return strings.Join(parts, " ")
}

// rlimitValue renders one limit in the field's own type — uint64 on
// most platforms, int64 on freebsd and dragonfly — as the number or
// "unlimited", the infinity judged by the platform's own spelling
// (rlimitUnlimited: -1 on linux, -3 on solaris, the int64 maximum on
// darwin, the BSDs and aix).
func rlimitValue[T ~int64 | ~uint64](v T) string {
	if rlimitUnlimited(v) {
		return "unlimited"
	}
	return fmt.Sprintf("%d", v)
}
