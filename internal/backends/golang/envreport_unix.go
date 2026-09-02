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

func rlimitValue(v uint64) string {
	if v == ^uint64(0) {
		return "unlimited"
	}
	return fmt.Sprintf("%d", v)
}
