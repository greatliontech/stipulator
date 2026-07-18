package golang

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/stipulate"
)

// buildResolverCLI builds the real stipulator binary: the owned client's
// protocol tests run against the production child — the hidden resolver
// subcommand wired through the CLI — never a stub loop.
func buildResolverCLI(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "stipulator")
	build := exec.Command("go", "build", "-o", bin, "github.com/greatliontech/stipulator/cmd/stipulator")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the CLI: %v\n%s", err, out)
	}
	return bin
}

// TestGoLoadPinsAmbientPackageDriverOff pins the load-environment half of
// the ambient-driver clause: symbol loading constructed outside policy
// normalization — the bind/verify/check/MCP construction path — runs with
// GOPACKAGESDRIVER pinned off, so an ambient driver that would poison
// resolution is never consulted and resolution goes through the real
// toolchain. The ambient driver deliberately points at a path that cannot
// execute: were it consulted, the load would fail loudly.
//
// Deliberately not //gofresh:pure: the verdict depends on module sources
// outside this binary's closure, loaded through go/packages children the
// testlog cannot observe.
func TestGoLoadPinsAmbientPackageDriverOff(t *testing.T) {
	stipulate.Covers(t, "REQ-go-owned-processes")
	t.Setenv("GOPACKAGESDRIVER", filepath.Join(t.TempDir(), "no-such-driver"))
	b, err := NewContext(context.Background(), "testdata/fixturemod")
	if err != nil {
		t.Fatalf("load consulted the ambient package driver: %v", err)
	}
	res, shape, err := b.Resolve("example.com/fixture/lib.Add")
	if err != nil {
		t.Fatal(err)
	}
	if res != verify.Resolved || len(shape) != 64 {
		t.Fatalf("Resolve under ambient driver = %v shape %q, want resolved with a shape hash", res, shape)
	}
}

// TestResolverWireMappingsRoundTrip pins the protocol's value mappings
// over every enum member — including the proof class, which the fixture
// module cannot produce — so no member can silently alias another across
// the wire.
//
//gofresh:pure
func TestResolverWireMappingsRoundTrip(t *testing.T) {
	stipulate.Covers(t, "REQ-go-owned-processes")
	for _, r := range []verify.Resolution{verify.Unverified, verify.Resolved, verify.NotFound, verify.GeneratedFile} {
		got, ok := resolutionFromWire(resolutionWire(r))
		if !ok || got != r {
			t.Errorf("resolution %v round-tripped to %v (known=%v)", r, got, ok)
		}
	}
	if _, ok := resolutionFromWire("bogus"); ok {
		t.Error("unknown resolution accepted")
	}
	for _, c := range []verify.WitnessClass{verify.ExampleWitness, verify.PropertyWitness, verify.AnalyzerProof} {
		got, ok := classFromWire(classWire(c))
		if !ok || got != c {
			t.Errorf("class %v round-tripped to %v (known=%v)", c, got, ok)
		}
	}
	if _, ok := classFromWire("bogus"); ok {
		t.Error("unknown class accepted")
	}
}

// TestOwnedResolverProtocolRoundTrips pins the owned client against the
// real resolver child over the fixture module: resolution outcomes,
// shape hashes, load-error text, witness classes, and slices all match
// the in-process backend's answers exactly — the child is the same
// implementation behind an owned process boundary, never a variant.
//
// Deliberately not //gofresh:pure: the verdict depends on module sources
// outside this binary's closure, loaded through go/packages children the
// testlog cannot observe.
func TestOwnedResolverProtocolRoundTrips(t *testing.T) {
	stipulate.Covers(t, "REQ-go-owned-processes")
	if testing.Short() {
		t.Skip("builds the CLI and spawns a resolver child")
	}
	bin := buildResolverCLI(t)
	dir, err := filepath.Abs(filepath.Join("testdata", "fixturemod"))
	if err != nil {
		t.Fatal(err)
	}
	inproc, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	owned := NewOwnedCommand(ctx, bin, ResolverSubcommand, dir)
	defer owned.Close()

	for _, symbol := range []string{
		"example.com/fixture/lib.Add",
		"example.com/fixture/lib.W",
		"example.com/fixture/lib.NoSuch",
		"example.com/fixture/nosuchpkg.X",
		"example.com/fixture/broken.F",
	} {
		wantRes, wantShape, wantErr := inproc.Resolve(symbol)
		gotRes, gotShape, gotErr := owned.Resolve(symbol)
		if gotRes != wantRes || gotShape != wantShape {
			t.Errorf("Resolve(%s) = (%v, %q), in-process (%v, %q)", symbol, gotRes, gotShape, wantRes, wantShape)
		}
		switch {
		case (wantErr == nil) != (gotErr == nil):
			t.Errorf("Resolve(%s) err = %v, in-process %v", symbol, gotErr, wantErr)
		case wantErr != nil && gotErr.Error() != wantErr.Error():
			t.Errorf("Resolve(%s) err text %q, in-process %q", symbol, gotErr, wantErr)
		}
	}

	for _, symbol := range []string{
		"example.com/fixture/lib.TestAdd",
		"example.com/fixture/lib.TestPropRapidCheck",
		"example.com/fixture/lib.TestPropRapidMakeCheck",
		"example.com/fixture/lib.F",
	} {
		if got, want := owned.WitnessClass(symbol), inproc.WitnessClass(symbol); got != want {
			t.Errorf("WitnessClass(%s) = %v, in-process %v", symbol, got, want)
		}
	}

	wantDecls, wantErr := inproc.Slice([]string{"example.com/fixture/lib.W"})
	if wantErr != nil {
		t.Fatal(wantErr)
	}
	gotDecls, gotErr := owned.Slice([]string{"example.com/fixture/lib.W"})
	if gotErr != nil {
		t.Fatal(gotErr)
	}
	if !reflect.DeepEqual(gotDecls, wantDecls) {
		t.Errorf("Slice through the child = %+v, in-process %+v", gotDecls, wantDecls)
	}
}

// TestOwnedResolverLoadErrorPropagates pins load-error propagation
// through the owned boundary: an unloadable tree surfaces from the first
// use as a verification error carrying the child's load-error text —
// exactly the error the in-process load reports — and the fault is
// sticky, never a silent absence (REQ-go-static-binding).
//
// Deliberately not //gofresh:pure: the verdict depends on go/packages
// children the testlog cannot observe.
func TestOwnedResolverLoadErrorPropagates(t *testing.T) {
	stipulate.Covers(t, "REQ-go-owned-processes", "REQ-go-static-binding")
	if testing.Short() {
		t.Skip("builds the CLI and spawns a resolver child")
	}
	bin := buildResolverCLI(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := New(dir); err == nil {
		t.Fatal("fixture loads in-process; the scenario no longer exercises a load error")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	owned := NewOwnedCommand(ctx, bin, ResolverSubcommand, dir)
	defer owned.Close()
	_, _, err := owned.Resolve("example.com/x.Y")
	if err == nil {
		t.Fatal("unloadable tree resolved without error through the child")
	}
	if !strings.Contains(err.Error(), "loading Go packages") {
		t.Errorf("error does not carry the load failure: %v", err)
	}
	_, _, again := owned.Resolve("example.com/x.Y")
	if again == nil {
		t.Fatal("load fault was not sticky")
	}
}
