package cmd

import (
	"context"
	"errors"
	"io"
	"testing"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/prune"
	"github.com/greatliontech/stipulator/internal/prunetest"
	"github.com/greatliontech/stipulator/stipulate"
)

func TestPruneStorePrintsCompletedAccountBeforeInterruption(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-store-gc")
	oldCollect, oldDir := collectStore, chdir
	t.Cleanup(func() { collectStore, chdir = oldCollect, oldDir })
	for _, resolution := range []bool{false, true} {
		name := "witness"
		if resolution {
			name = "resolution"
		}
		t.Run(name, func(t *testing.T) {
			f := prunetest.New(t, resolution)
			collectStore = func(ctx context.Context, d prune.Deps) (prune.StoreResult, error) {
				d.Capture = f.Capture
				return prune.StoreGC(ctx, d)
			}
			ctx, cancel := f.Context(t.Context())
			defer cancel()
			out, err := captureStdout(t, func() error {
				return execute(ctx, []string{"-C", f.Root, "prune", "--store"}, func(*stipulatorv1.ProgressEvent) {}, io.Discard)
			})
			var interrupted Interrupted
			if !errors.Is(err, context.Canceled) || !errors.As(err, &interrupted) {
				t.Fatalf("partial operation returned %v", err)
			}
			want := "store gc: completed prefix; unexamined records are not counted as kept\nstore gc: 1 record variant(s) removed, 2 kept\n"
			if resolution {
				want = "store gc: completed prefix; unexamined records are not counted as kept\nstore gc: 3 record variant(s) removed, 0 kept\nstore gc: 1 resolution record(s) removed, 2 kept\n"
			}
			if out != want {
				t.Fatalf("account=%q; want %q", out, want)
			}
			f.CheckPrefix(t)
		})
	}
}
