package recordstore

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/greatliontech/stipulator/stipulate"
)

// TestPruneKindAbandonedBusyTemporariesAndBoundedGrowth pins global recordstore
// retention across multiple corpus roots:
//   - abandoned corpus stores (whose recorded coordinate path no longer exists)
//     are reclaimed in full while preserving their sibling .lock inode;
//   - busy stores holding a store lock are reported in BusyStores and spared;
//   - dot-prefixed active publication temporaries are never deleted;
//   - MaxBytes bounds total retained bytes across live corpus roots oldest-first.
func TestPruneKindAbandonedBusyTemporariesAndBoundedGrowth(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-record-store-layout")
	cacheDir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheDir)

	abandonedRoot := filepath.Join(t.TempDir(), "abandoned-worktree")
	if err := os.MkdirAll(abandonedRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	liveRootOld := filepath.Join(t.TempDir(), "live-old")
	liveRootNew := filepath.Join(t.TempDir(), "live-new")
	busyRoot := filepath.Join(t.TempDir(), "busy-worktree")
	for _, dir := range []string{liveRootOld, liveRootNew, busyRoot} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	abandonedStore, err := Open("witnesses", abandonedRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := abandonedStore.Install(4, Entry{Name: mustName(t, []string{"pkg", "TestAbandoned"}, 1), Data: []byte("abandoned-payload")}); err != nil {
		t.Fatal(err)
	}
	// Create a sibling lock file before removing the worktree to verify the
	// lock inode is preserved across abandoned-store reclamation.
	abandonedLock, err := abandonedStore.Lock(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	lockInfoBefore, err := abandonedLock.Stat()
	if err != nil {
		t.Fatal(err)
	}
	_ = abandonedLock.Close()
	if err := os.RemoveAll(abandonedRoot); err != nil {
		t.Fatal(err)
	}

	oldStore, err := Open("witnesses", liveRootOld)
	if err != nil {
		t.Fatal(err)
	}
	oldName := mustName(t, []string{"pkg", "TestOld"}, 1)
	if err := oldStore.Install(4, Entry{Name: oldName, Data: []byte("0123456789abcdef")}); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Unix(1_000_000, 0)
	_ = os.Chtimes(filepath.Join(oldStore.Path(), oldName), oldTime, oldTime)
	// Place an active dot-prefixed temporary in oldStore; it must survive.
	tmpFile := filepath.Join(oldStore.Path(), ".witnesses-inflight.json")
	if err := os.WriteFile(tmpFile, []byte("inflight"), 0o644); err != nil {
		t.Fatal(err)
	}

	newStore, err := Open("witnesses", liveRootNew)
	if err != nil {
		t.Fatal(err)
	}
	newName := mustName(t, []string{"pkg", "TestNew"}, 1)
	if err := newStore.Install(4, Entry{Name: newName, Data: []byte("fedcba9876543210")}); err != nil {
		t.Fatal(err)
	}
	newTime := time.Unix(2_000_000, 0)
	_ = os.Chtimes(filepath.Join(newStore.Path(), newName), newTime, newTime)

	busyStore, err := Open("witnesses", busyRoot)
	if err != nil {
		t.Fatal(err)
	}
	busyName := mustName(t, []string{"pkg", "TestBusy"}, 1)
	if err := busyStore.Install(4, Entry{Name: busyName, Data: []byte("busy-payload-data")}); err != nil {
		t.Fatal(err)
	}
	heldLock, err := busyStore.Lock(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer heldLock.Close()

	// Preview (DryRun) reclaims abandonedStore and oldStore to fit within 40 bytes,
	// reports busyStore as busy, and deletes nothing on disk.
	preview, err := PruneKind(t.Context(), "witnesses", PruneOptions{MaxBytes: 40, DryRun: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if preview.AbandonedStores != 1 || preview.BusyStores < 1 || preview.RemovedFiles != 2 {
		t.Fatalf("unexpected preview report: %+v", preview)
	}
	if _, err := os.Stat(filepath.Join(oldStore.Path(), oldName)); err != nil {
		t.Fatalf("DryRun deleted old record: %v", err)
	}

	// Real reclamation.
	rep, err := PruneKind(t.Context(), "witnesses", PruneOptions{MaxBytes: 40}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rep.AbandonedStores != 1 || rep.BusyStores < 1 || rep.RemovedFiles != 2 || rep.ReclaimedBytes != preview.ReclaimedBytes {
		t.Fatalf("unexpected prune report: %+v (preview %+v)", rep, preview)
	}
	// Sibling lock inode of abandonedStore is still present and unchanged.
	lockInfoAfter, err := os.Stat(abandonedStore.Path() + ".lock")
	if err != nil || !os.SameFile(lockInfoBefore, lockInfoAfter) {
		t.Fatalf("abandoned store sibling .lock inode was not preserved: err=%v", err)
	}
	// Active temporary in oldStore was spared while oldName was evicted.
	if _, err := os.Stat(tmpFile); err != nil {
		t.Fatalf("active temporary was removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(oldStore.Path(), oldName)); !os.IsNotExist(err) {
		t.Fatalf("expected oldName to be evicted, got err=%v", err)
	}
	// Newer live store and busy store both kept their records.
	if _, err := os.Stat(filepath.Join(newStore.Path(), newName)); err != nil {
		t.Fatalf("expected newName to be kept: %v", err)
	}
	if _, err := os.Stat(filepath.Join(busyStore.Path(), busyName)); err != nil {
		t.Fatalf("expected busyName to be kept: %v", err)
	}
}
