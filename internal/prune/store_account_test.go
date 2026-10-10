package prune

import (
	"context"
	"errors"
	"os"
	"reflect"
	"testing"

	"github.com/greatliontech/stipulator/internal/prunetest"
	"github.com/greatliontech/stipulator/internal/records"
	"github.com/greatliontech/stipulator/internal/verbcore"
	"github.com/greatliontech/stipulator/stipulate"
)

func TestStoreGCKeepsCompletedCountsOnCancellation(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-store-gc")
	for _, resolution := range []bool{false, true} {
		name := "witness"
		if resolution {
			name = "resolution"
		}
		t.Run(name, func(t *testing.T) {
			f := prunetest.New(t, resolution)
			ctx, cancel := f.Context(t.Context())
			defer cancel()
			got, err := StoreGC(ctx, Deps{Root: f.Root, Deps: verbcore.Deps{Capture: f.Capture}, Load: func() (*records.Store, error) { return records.Load(os.DirFS(f.Root)) }})
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("collection lost cancellation: %v", err)
			}
			want := StoreResult{Removed: 1, Kept: 2}
			if resolution {
				want = StoreResult{Removed: 3, Kept: 0, Resolutions: &ResolutionCounts{Removed: 1, Kept: 2}}
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("account=%+v resolutions=%+v; want %+v resolutions=%+v", got, got.Resolutions, want, want.Resolutions)
			}
			f.CheckPrefix(t)
		})
	}
}
