// Package verbcore holds what every verb core takes from a face: the
// corpus preparation, the policy capture, the backend set, and the
// witness run. A face builds one Deps and hands it to
// each core it renders; the cores compute and never write, and read
// the progress reporter the face put on the context (none on the CLI).
package verbcore

import (
	"context"

	"github.com/greatliontech/gofresh"

	"github.com/greatliontech/stipulator/internal/backends/golang"
	"github.com/greatliontech/stipulator/internal/check"
	"github.com/greatliontech/stipulator/internal/verify"
)

// Deps is what a face supplies to a verb core.
type Deps struct {
	// Prepare compiles the corpus and loads the records (the face's
	// preparation, with its own diagnostics rendering).
	Prepare func() (*check.Prepared, error)
	// Capture loads the accepted policy for a witnessed operation.
	Capture func(context.Context) (*golang.Capture, error)
	// Backends builds the verification backends over the operation's
	// symbols; the core releases them.
	Backends func(context.Context, []string) (map[string]verify.Backend, error)
	// RunTests executes the witness run — the whole policy when scope
	// is nil, the scoped selection otherwise; why names a scope for a
	// face that announces it.
	RunTests func(ctx context.Context, pc *golang.Capture, seeding verify.WitnessSeeding, scope map[gofresh.Subject]bool, why string) (*verify.TestRun, error)
}
