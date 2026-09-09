//go:build unix

package golang

import (
	"context"
	"testing"

	"github.com/greatliontech/stipulator/stipulate"
)

// The declaration-reading forwarders surface the child's fault: a
// preview distinguishes "does not resolve" from "the boundary died"
// only through the error, so a dead child errors SymbolFile and
// ReachedPackages rather than answering absence (REQ-go-owned-processes).
//
//gofresh:pure
func TestWholeTreeForwardersSurfaceTheChildsFault(t *testing.T) {
	stipulate.Covers(t, "REQ-go-owned-processes")
	whole, err := NewWholeTree(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	whole.child = newResolverClientCommand(context.Background(), "/bin/sh", "-c", "exit 1")
	defer whole.Close()
	if _, ok, err := whole.SymbolFile("example.com/p.F"); err == nil || ok {
		t.Fatalf("SymbolFile over a dead child = %v, %v; want the fault", ok, err)
	}
	if reach, err := whole.ReachedPackages([]string{"p/p.go"}); err == nil || len(reach) != 0 {
		t.Fatalf("ReachedPackages over a dead child = %v, %v; want the fault", reach, err)
	}
}
