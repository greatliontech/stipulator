package golang

import (
	"fmt"
	"regexp"
	"strings"
)

// Direct reference fixtures sample the toolchain under a contained probe.
// Native production owns its classification roots inside the Gofresh session.
var rootsRunner = containedRunner(boundProbe)

// testCommandArgs renders the direct cmd/go reference used by differential
// execution tests. Production executes only retained artifacts; this control
// deliberately asks Go itself to perform flag translation and execution.
func testCommandArgs(n *NormalizedInvocation, pkg string, selection []string, logPath string) []string {
	args := []string{"test", "-json", "-timeout=0"}
	args = append(args, buildFlags(n.Race, n.Tags, n.ModuleMode, buildPGO(treeRoot(n), n.PGO))...)
	switch {
	case n.CacheBypass:
		args = append(args, "-count=1")
	case n.Count > 0:
		args = append(args, fmt.Sprintf("-count=%d", n.Count))
	}
	if len(selection) > 0 {
		quoted := make([]string, len(selection))
		for i, name := range selection {
			quoted[i] = regexp.QuoteMeta(name)
		}
		args = append(args, "-run=^("+strings.Join(quoted, "|")+")$")
	}
	args = append(args, pkg)
	if logPath != "" || len(n.Args) > 0 {
		args = append(args, "-args")
		if logPath != "" {
			args = append(args, "-test.testlogfile="+logPath)
		}
		args = append(args, n.Args...)
	}
	return args
}
