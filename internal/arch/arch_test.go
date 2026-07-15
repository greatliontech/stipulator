// Package arch holds the repository's structural proofs: analyzer
// assertions over the import graph and interface surfaces, executed as
// ordinary tests and scored as the proof evidence class.
package arch

import (
	"testing"

	surfacewire "github.com/greatliontech/stipulator/bindingsurface"
	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/backends/golang"
	rootbindingsurface "github.com/greatliontech/stipulator/internal/bindingsurface"
	"github.com/greatliontech/stipulator/internal/records"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/stipulate"
	"github.com/greatliontech/stipulator/stipulate/structural"
)

const mod = "github.com/greatliontech/stipulator"

// TestCoreNeverImportsBackends proves backend neutrality: the compilation,
// binding, coverage, and change models never depend on a backend —
// backend knowledge is confined to symbol interpretation and proving.
//
// Deliberately not //gofresh:pure: the verdict depends on the import
// graph of module packages outside this test binary's closure, read
// through a go list child the testlog cannot observe. A cached pass
// could serve while an audited package drifts; the witness re-runs
// every gate.
func TestCoreNeverImportsBackends(t *testing.T) {
	stipulate.Covers(t, "REQ-backend-core-neutral")
	for _, core := range []string{
		mod + "/internal/canon", mod + "/internal/corpus", mod + "/internal/profile",
		mod + "/internal/compile", mod + "/internal/records", mod + "/internal/author",
		mod + "/internal/verify", mod + "/internal/coverage", mod + "/internal/diff",
		mod + "/internal/bundle", mod + "/internal/facts",
	} {
		structural.NoImport(t, core, mod+"/internal/backends/...")
	}
}

// TestCoreIsVcsFree proves the VCS-independence invariant: no core package
// reads version-control state or shells out — revisions enter only as
// trees. The Go backend is the one sanctioned toolchain exception (go
// test, go/packages), and command wiring may exec; neither is core.
//
// Deliberately not //gofresh:pure: the verdict depends on the import
// graph of module packages outside this test binary's closure, read
// through a go list child the testlog cannot observe. A cached pass
// could serve while an audited package drifts; the witness re-runs
// every gate.
func TestCoreIsVcsFree(t *testing.T) {
	stipulate.Covers(t, "REQ-core-vcs-free")
	for _, core := range []string{
		mod + "/internal/canon", mod + "/internal/corpus", mod + "/internal/profile",
		mod + "/internal/compile", mod + "/internal/records", mod + "/internal/author",
		mod + "/internal/verify", mod + "/internal/coverage", mod + "/internal/diff",
		mod + "/internal/bundle", mod + "/internal/facts",
	} {
		structural.NoImport(t, core,
			"os/exec",
			"github.com/go-git/go-git/...",
			"github.com/go-git/go-billy/...",
		)
	}
}

// TestBackendSatisfiesVerifierSurfaces proves the Go backend's optional
// surfaces are real interface satisfactions, not naming coincidences.
//
//gofresh:pure
func TestBackendSatisfiesVerifierSurfaces(t *testing.T) {
	stipulate.Covers(t, "REQ-go-structural-provers")
	structural.Implements[verify.Backend](t, (*golang.Backend)(nil))
	structural.Implements[verify.Slicer](t, (*golang.Backend)(nil))
	structural.Implements[verify.WitnessClassifier](t, (*golang.Backend)(nil))
	structural.Implements[verify.VacuityChecker](t, (*golang.Backend)(nil))
}

// TestBindingSurfaceWireOwnership proves that root derivation returns the
// shared wire type while the wire module remains independent of applications.
//
// Deliberately not //gofresh:pure: the import-graph verdict depends on packages
// outside this test binary's closure and must be re-evaluated every gate.
func TestBindingSurfaceWireOwnership(t *testing.T) {
	stipulate.Covers(t, "REQ-advisory-go-wire")
	structural.FunctionSignature[func(*stipulatorv1.Spec, *records.Store) (*surfacewire.Report, error)](t, rootbindingsurface.Derive)
	structural.NoImport(t, mod+"/bindingsurface",
		mod+"/cmd/...",
		mod+"/gen/...",
		mod+"/internal/...",
		mod+"/stipulate/...",
		"github.com/greatliontech/gomutant/...",
	)
}
