//go:build unix

package golang

import (
	"strings"
	"testing"

	"github.com/greatliontech/stipulator/stipulate"
)

// TestGoEnvDivergenceReportLimits pins the process-limits clause on
// the platforms that render it: the report carries the limits line
// with the descriptor limit the witness children inherit.
//
//gofresh:pure
func TestGoEnvDivergenceReportLimits(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-flip-environment")
	n := &NormalizedInvocation{Dir: "/tree", Env: []string{"A=1"}, WitnessEnv: []string{"A=1", "GOMAXPROCS=1"}, Ambient: []string{"A=1"}}
	rep := envDivergenceReport(n, "")
	if !strings.Contains(rep, "process limits: ") || !strings.Contains(rep, "nofile=") {
		t.Errorf("limits line absent from the report:\n%s", rep)
	}
}
