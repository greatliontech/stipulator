package golang

import (
	"context"
	"flag"
	"os"
	"testing"
)

// TestMain routes resolver-child re-execs — a resolver client self-execs
// os.Executable(), this binary — and loads the shared repository-tree
// backend: after flags parse, before m.Run installs the testlog, so
// the tree read stays outside every witness's observation exactly as
// a package-init load kept it, and only on the full tier — the fast
// tier gates every test that reads it.
func TestMain(m *testing.M) {
	ResolverChildMain()
	flag.Parse()
	if !testing.Short() {
		b, err := newContext(context.Background(), "../../..", nil)
		if err != nil {
			panic(err)
		}
		backend = b
	}
	os.Exit(m.Run())
}
