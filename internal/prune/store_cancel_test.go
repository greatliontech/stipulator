package prune

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/guard"
	"github.com/greatliontech/stipulator/internal/backends/golang"
	"github.com/greatliontech/stipulator/internal/check"
	"github.com/greatliontech/stipulator/internal/records"
	"github.com/greatliontech/stipulator/internal/witnesscache"
	"github.com/greatliontech/stipulator/stipulate"
)

func TestStoreGCPropagatesCancellationFromPolicyCapture(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-store-gc")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	rec := witnesscache.Record{Group: "group", Package: "example.com/p", Test: "TestA", Outcomes: map[string]string{"example.com/p.TestA": "passed"}, Fingerprint: gofresh.Fingerprint{
		MaximalClosure: strings.Repeat("a", 32), TestVariantClosure: strings.Repeat("b", 32), ClosureStrategy: gofresh.ClosureStrategy, DynamicStateStrategy: gofresh.DynamicStateStrategy,
		Guards: guard.Guards{Toolchain: "go1.27.2", BuildConfig: strings.Repeat("c", 32)}, RuntimeInputs: "eyJ2IjoyfQ", RuntimeDigest: "3a79bf37b571938d1f2907afb6a643f4", ResultKind: gofresh.CodeResult,
	}}
	if err := witnesscache.Install(t.Context(), dir, rec); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	deps := untouchable(t, &check.Prepared{Store: &records.Store{}})
	deps.Root = dir
	deps.Capture = func(received context.Context) (*golang.Capture, error) {
		if received != ctx {
			t.Fatal("capture received another context")
		}
		cancel()
		return nil, received.Err()
	}
	if _, err := StoreGC(ctx, deps); !errors.Is(err, context.Canceled) {
		t.Fatalf("prune continued after cancelled capture: %v", err)
	}
	if got := witnesscache.Load(t.Context(), dir); len(got) != 1 {
		t.Fatal("cancelled prune deleted the record", got)
	}
}
