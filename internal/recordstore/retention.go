package recordstore

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// DefaultMaxBytes is the default global byte retention ceiling across
// stipulator's machine-local record stores (2 GiB), matching gofresh's
// DefaultMemoMaxBytes.
const DefaultMaxBytes int64 = 2 << 30

var (
	maxBytesMu sync.RWMutex
	maxBytes   = DefaultMaxBytes
)

// SetMaxBytes configures the default global byte retention ceiling. Zero
// restores DefaultMaxBytes; a negative value disables cross-store byte
// eviction while leaving abandoned-corpus reclamation active.
func SetMaxBytes(limit int64) {
	if limit == 0 {
		limit = DefaultMaxBytes
	}
	maxBytesMu.Lock()
	maxBytes = limit
	maxBytesMu.Unlock()
}

// MaxBytes returns the configured global byte retention ceiling.
func MaxBytes() int64 {
	maxBytesMu.RLock()
	defer maxBytesMu.RUnlock()
	return maxBytes
}

// PruneOptions configures a global usage/preview or reclamation pass over a
// record-store kind.
type PruneOptions struct {
	// MaxBytes bounds the retained bytes across non-busy stores when >= 0.
	// A negative value reclaims only abandoned corpus stores and unreferenced
	// substore artifacts without evicting records from live corpora.
	MaxBytes int64
	// DryRun computes usage and preview counts without deleting any files.
	DryRun bool
	// ActiveRoots is an optional set of canonical corpus coordinates that must
	// never be treated as abandoned even if not yet materialized on disk.
	ActiveRoots map[string]bool
}

// PruneReport summarizes one global usage/preview or reclamation pass.
type PruneReport struct {
	Root            string
	UsedBytes       int64
	ReclaimedBytes  int64
	RemainingBytes  int64
	RemovedFiles    int
	AbandonedStores int
	BusyStores      int
	SkippedEntries  int
}

// RecordEvictHook is invoked under the parent store's lock when a live record
// file is evicted during MaxBytes reclamation (or previewed in DryRun),
// returning any substore bytes and files orphaned by that record's eviction.
type RecordEvictHook func(ctx context.Context, recordName string, dryRun bool) (reclaimed int64, removed, skipped int, err error)

// SubstoreHook coordinates a nested substore (such as witnesscache's ledgers
// directory) under the parent store's lock. When abandoned is true, all
// substore records belong to an abandoned worktree and are eligible for
// reclamation; otherwise unreferenced substore entries are reclaimed and an
// optional RecordEvictHook is returned to account for substore entries orphaned
// when live parent records are subsequently evicted by MaxBytes.
type SubstoreHook func(ctx context.Context, s Store, abandoned, dryRun bool) (used, reclaimed int64, removed, skipped int, onEvict RecordEvictHook, err error)

// ListStores returns every corpus store directory under Root(kind), populated
// with its recorded canonical corpus coordinate when present.
func ListStores(kind string) ([]Store, error) {
	root, err := Root(kind)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var stores []Store
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		s := Store{kind: kind, path: filepath.Join(root, e.Name())}
		s.coord = s.Coordinate()
		stores = append(stores, s)
	}
	return stores, nil
}

// Abandoned reports whether s records a canonical corpus coordinate whose
// directory no longer exists on disk and is not in activeRoots.
func (s Store) Abandoned(activeRoots map[string]bool) bool {
	coord := s.Coordinate()
	if coord == "" || activeRoots[coord] {
		return false
	}
	_, err := os.Stat(coord)
	return errors.Is(err, fs.ErrNotExist)
}

type storeFile struct {
	storeIdx int
	name     string
	path     string
	size     int64
	modTime  time.Time
}

func measureDirJSONBytes(dir string) (used int64, skipped int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			skipped++
		}
		return 0, skipped
	}
	for _, e := range entries {
		if e.IsDir() {
			subUsed, subSkipped := measureDirJSONBytes(filepath.Join(dir, e.Name()))
			used += subUsed
			skipped += subSkipped
			continue
		}
		if strings.HasPrefix(e.Name(), ".") || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				skipped++
			}
			continue
		}
		used += info.Size()
	}
	return used, skipped
}

// PruneKind performs global usage/preview and bounded reclamation across all
// corpus stores of kind under the user cache directory. Sibling .lock inodes
// and dot-prefixed active publication temporaries are always preserved, and
// contended stores are reported in BusyStores rather than waited on.
func PruneKind(ctx context.Context, kind string, opts PruneOptions, substore SubstoreHook) (PruneReport, error) {
	if err := ctx.Err(); err != nil {
		return PruneReport{}, err
	}
	root, err := Root(kind)
	if err != nil {
		return PruneReport{}, err
	}
	report := PruneReport{Root: root}
	stores, err := ListStores(kind)
	if err != nil {
		return report, err
	}

	var liveStores []Store
	var evictHooks []RecordEvictHook
	var candidates []storeFile

	for _, s := range stores {
		if err := ctx.Err(); err != nil {
			report.RemainingBytes = report.UsedBytes - report.ReclaimedBytes
			return report, err
		}
		lock, lockErr := s.TryLock()
		if lockErr != nil {
			if errors.Is(lockErr, errLockContended) {
				report.BusyStores++
				used, skipped := measureDirJSONBytes(s.path)
				report.UsedBytes += used
				report.SkippedEntries += skipped
				continue
			}
			report.SkippedEntries++
			continue
		}

		abandoned := s.Abandoned(opts.ActiveRoots)
		if abandoned {
			report.AbandonedStores++
			entries, readErr := os.ReadDir(s.path)
			if readErr != nil && !errors.Is(readErr, fs.ErrNotExist) {
				report.SkippedEntries++
			}
			for _, e := range entries {
				if err := ctx.Err(); err != nil {
					_ = lock.Close()
					report.RemainingBytes = report.UsedBytes - report.ReclaimedBytes
					return report, err
				}
				if e.IsDir() || strings.HasPrefix(e.Name(), ".") || !strings.HasSuffix(e.Name(), ".json") {
					continue
				}
				info, infoErr := e.Info()
				if infoErr != nil {
					if !errors.Is(infoErr, fs.ErrNotExist) {
						report.SkippedEntries++
					}
					continue
				}
				size := info.Size()
				report.UsedBytes += size
				full := filepath.Join(s.path, e.Name())
				if opts.DryRun {
					report.ReclaimedBytes += size
					report.RemovedFiles++
					continue
				}
				if rmErr := os.Remove(full); rmErr != nil && !errors.Is(rmErr, fs.ErrNotExist) {
					report.SkippedEntries++
					continue
				}
				report.ReclaimedBytes += size
				report.RemovedFiles++
			}
			if substore != nil {
				subUsed, subReclaimed, subRemoved, subSkipped, _, _ := substore(ctx, s, true, opts.DryRun)
				report.UsedBytes += subUsed
				report.ReclaimedBytes += subReclaimed
				report.RemovedFiles += subRemoved
				report.SkippedEntries += subSkipped
			}
			if err := ctx.Err(); err != nil {
				_ = lock.Close()
				report.RemainingBytes = report.UsedBytes - report.ReclaimedBytes
				return report, err
			}
			if !opts.DryRun {
				_ = os.Remove(s.path + ".root")
				_ = os.Remove(s.path)
			}
			_ = lock.Close()
			continue
		}

		liveIdx := len(liveStores)
		liveStores = append(liveStores, s)
		entries, readErr := os.ReadDir(s.path)
		if readErr != nil && !errors.Is(readErr, fs.ErrNotExist) {
			report.SkippedEntries++
		}
		for _, e := range entries {
			if e.IsDir() || strings.HasPrefix(e.Name(), ".") || !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			info, infoErr := e.Info()
			if infoErr != nil {
				if !errors.Is(infoErr, fs.ErrNotExist) {
					report.SkippedEntries++
				}
				continue
			}
			size := info.Size()
			report.UsedBytes += size
			candidates = append(candidates, storeFile{
				storeIdx: liveIdx,
				name:     e.Name(),
				path:     filepath.Join(s.path, e.Name()),
				size:     size,
				modTime:  info.ModTime(),
			})
		}
		var onEvict RecordEvictHook
		if substore != nil {
			var subUsed, subReclaimed int64
			var subRemoved, subSkipped int
			subUsed, subReclaimed, subRemoved, subSkipped, onEvict, _ = substore(ctx, s, false, opts.DryRun)
			report.UsedBytes += subUsed
			report.ReclaimedBytes += subReclaimed
			report.RemovedFiles += subRemoved
			report.SkippedEntries += subSkipped
		}
		evictHooks = append(evictHooks, onEvict)
		_ = lock.Close()
		if err := ctx.Err(); err != nil {
			report.RemainingBytes = report.UsedBytes - report.ReclaimedBytes
			return report, err
		}
	}

	report.RemainingBytes = report.UsedBytes - report.ReclaimedBytes
	if opts.MaxBytes < 0 || report.RemainingBytes <= opts.MaxBytes {
		return report, ctx.Err()
	}

	slices.SortFunc(candidates, func(a, b storeFile) int {
		if !a.modTime.Equal(b.modTime) {
			if a.modTime.Before(b.modTime) {
				return -1
			}
			return 1
		}
		return strings.Compare(a.path, b.path)
	})

	busyLive := map[int]bool{}
	for _, f := range candidates {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		if report.RemainingBytes <= opts.MaxBytes {
			break
		}
		if busyLive[f.storeIdx] {
			continue
		}
		s := liveStores[f.storeIdx]
		lock, lockErr := s.TryLock()
		if lockErr != nil {
			busyLive[f.storeIdx] = true
			if errors.Is(lockErr, errLockContended) {
				report.BusyStores++
			} else {
				report.SkippedEntries++
			}
			continue
		}
		if !opts.DryRun {
			if rmErr := os.Remove(f.path); rmErr != nil && !errors.Is(rmErr, fs.ErrNotExist) {
				report.SkippedEntries++
				_ = lock.Close()
				continue
			}
		}
		report.ReclaimedBytes += f.size
		report.RemainingBytes -= f.size
		report.RemovedFiles++
		if onEvict := evictHooks[f.storeIdx]; onEvict != nil {
			subReclaimed, subRemoved, subSkipped, evictErr := onEvict(ctx, f.name, opts.DryRun)
			report.ReclaimedBytes += subReclaimed
			report.RemainingBytes -= subReclaimed
			report.RemovedFiles += subRemoved
			report.SkippedEntries += subSkipped
			if evictErr != nil && ctx.Err() != nil {
				_ = lock.Close()
				return report, ctx.Err()
			}
		}
		_ = lock.Close()
	}

	return report, ctx.Err()
}
