//go:build unix

package golang

import (
	"context"
	"strings"
	"testing"

	"github.com/greatliontech/stipulator/internal/resolutioncache"
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

// TestServedPublishNamesAChildFaultInTheClassification pins the one arm
// of the publish that was silent (REQ-evidence-freshness-degrade): a
// child fault while classifying the pending symbols publishes nothing
// under the key and the degraded notices say so.
func TestServedPublishNamesAChildFaultInTheClassification(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-freshness-degrade")
	if testing.Short() {
		t.Skip("loads a fixture module's types and views")
	}
	neutralAmbient(t)
	dir := servedModule(t)
	ctx := context.Background()
	s, err := NewServed(ctx, dir, servedSymbols[:1])
	if err != nil {
		t.Fatal(err)
	}
	// Resolved through the real child, so the symbol pends publication;
	// then the child is replaced by one that answers nothing.
	if _, _, _, err := s.ResolveIn(servedSymbols[0]); err != nil {
		t.Fatal(err)
	}
	s.child.Close()
	s.child = newResolverClientCommand(ctx, "/bin/sh", "-c", "exit 1")
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	degraded := strings.Join(s.Degraded(), "\n")
	if !strings.Contains(degraded, "never-serve classification") {
		t.Fatalf("a child fault in the publish's classification went unsaid; degraded %q, notices %v", degraded, s.Notices())
	}
	if len(resolutioncache.Load(dir)) != 0 {
		t.Fatal("a record was published past a faulted classification")
	}
}
