package cmd

import (
	"context"
	"testing"

	"github.com/spf13/cobra"

	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/stipulate"
)

// recordingBackend stands in for the whole-tree backend: it answers
// nothing and records whether the verb closed it.
type recordingBackend struct{ closed bool }

func (b *recordingBackend) Resolve(string) (verify.Resolution, string, error) {
	return verify.NotFound, "", nil
}

func (b *recordingBackend) Close() error { b.closed = true; return nil }

// Every verb that builds the declaration-reading backends closes them
// before it returns, on the error path included, so the resolver child
// dies with the verb and never outlives it into the process's end
// (REQ-go-owned-processes).
//
//gofresh:pure
func TestVerbsCloseTheirBackends(t *testing.T) {
	stipulate.Covers(t, "REQ-go-owned-processes")
	priorDir, priorMake := chdir, makeBackends
	chdir = t.TempDir()
	t.Cleanup(func() { chdir, makeBackends = priorDir, priorMake })
	// A scaffolded corpus: the bare pin compiles it before it builds
	// its backends; every verb still fails past that point, since no
	// record names anything.
	scaffold := initCmd()
	// A nil argument slice makes cobra read the process arguments —
	// under a test oracle, the oracle's own flags — so every in-process
	// command gets an explicit, possibly empty, slice.
	scaffold.SetArgs([]string{})
	if err := scaffold.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, verb := range []struct {
		name   string
		newCmd func() *cobra.Command
		args   []string
	}{
		{"bind", bindCmd, []string{"--req", "REQ-x", "--role", "tests", "--symbol", "example.com/p.TestX"}},
		{"pin bare", pinCmd, []string{}},
		{"pin --req", pinCmd, []string{"--req", "REQ-x"}},
		{"retarget", retargetCmd, []string{"--from", "example.com/old", "--to", "example.com/new"}},
	} {
		backend := &recordingBackend{}
		built := false
		makeBackends = func(context.Context, string) (map[string]verify.Backend, func() error, error) {
			built = true
			return map[string]verify.Backend{"go": backend}, backend.Close, nil
		}
		cmd := verb.newCmd()
		cmd.SetArgs(verb.args)
		// The scaffolded corpus holds no record, so past its build a
		// verb either returns a no-op success (the bare pin: every pin
		// current) or an error (the rest) — and each must have closed.
		_ = cmd.ExecuteContext(context.Background())
		if !built {
			t.Fatalf("%s: built no backend, the pin is vacuous", verb.name)
		}
		if !backend.closed {
			t.Fatalf("%s: returned without closing its backend", verb.name)
		}
	}
}
