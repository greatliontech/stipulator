package recordstore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// Lock excludes other processes and goroutines using this store's compound
// operations. The stable lock file is beside the store, so deleting a cache
// directory cannot replace the locked inode. Closing the returned file releases
// the lock; a process exit releases it too. A contended acquisition waits under
// ctx, never in a blocking OS lock call. Callers keep it through both their
// reference judgment and writes/deletions, never just through the snapshot.
func (s Store) Lock(ctx context.Context) (*os.File, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f, err := s.openLock()
	if err != nil {
		return nil, err
	}
	for {
		if err := ctx.Err(); err != nil {
			f.Close()
			return nil, err
		}
		err := tryLockFile(f)
		if err == nil {
			if err := ctx.Err(); err != nil {
				f.Close()
				return nil, err
			}
			return f, nil
		}
		if !errors.Is(err, errLockContended) {
			f.Close()
			return nil, err
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			f.Close()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

var errLockContended = errors.New("recordstore: store lock is held")

// TryLock attempts exclusion once. Opportunistic cleanup skips its work when
// another operation holds the store; it never queues behind that operation.
func (s Store) TryLock() (*os.File, error) {
	f, err := s.openLock()
	if err != nil {
		return nil, err
	}
	if err := tryLockFile(f); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

func (s Store) openLock() (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(s.path), 0755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(s.path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	return f, nil
}
