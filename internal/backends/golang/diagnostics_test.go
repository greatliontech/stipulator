package golang

import (
	"bytes"
	"testing"

	"github.com/greatliontech/gofresh"
)

// TestEngineDiagnosticsDeliverDetailEvents pins the diagnostic leg of
// the engine progress adapter: a payload-bearing gofresh event reaches
// the operator's log as one line, and detail-free keep-alives stay
// silent — the class of defect being a consumer that discards
// diagnostics by signature.
func TestEngineDiagnosticsDeliverDetailEvents(t *testing.T) {
	var buf bytes.Buffer
	old := engineDiagnostics
	engineDiagnostics = &buf
	defer func() { engineDiagnostics = old }()
	emitEngineDiagnostic(gofresh.Progress{Phase: "observe", Package: "example.com/x"})
	if buf.Len() != 0 {
		t.Fatalf("keep-alive wrote %q, want silence", buf.String())
	}
	emitEngineDiagnostic(gofresh.Progress{Phase: "analysis-unavailable", Package: "example.com/x", Detail: "unsupported analysis shape: chan T"})
	if got, want := buf.String(), "gofresh: analysis-unavailable example.com/x — unsupported analysis shape: chan T\n"; got != want {
		t.Fatalf("diagnostic line = %q, want %q", got, want)
	}
}
