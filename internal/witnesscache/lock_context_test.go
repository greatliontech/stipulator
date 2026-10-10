package witnesscache

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/greatliontech/gofresh/gotool"
	"github.com/greatliontech/stipulator/internal/recordstore"
	"github.com/greatliontech/stipulator/stipulate"
)

// Done is consulted at the lock's contended wait, not at its initial Err check.
// This handshake cancels an actual waiter rather than a not-yet-started call.
type lockWaitContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (c *lockWaitContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

func TestStoreOperationsCancelBehindAnotherProcess(t *testing.T) {
	if dir := os.Getenv("STIPULATOR_LOCK_HOLDER"); dir != "" {
		store, err := open(dir)
		if err != nil {
			t.Fatal(err)
		}
		lock, err := store.Lock(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer lock.Close()
		fmt.Println("held")
		var signal [1]byte
		os.Stdin.Read(signal[:])
		return
	}
	stipulate.Covers(t, "REQ-evidence-witness-cache-format-ledger", "REQ-evidence-store-gc")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	rec := reusedLedgerRecord()
	if err := Install(t.Context(), dir, rec); err != nil {
		t.Fatal(err)
	}
	store, err := open(dir)
	if err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(store.Path(), "ledgers", "orphan.json")
	if err := os.WriteFile(orphan, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(orphan, past, past); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestStoreOperationsCancelBehindAnotherProcess$", "-test.count=1")
	child.Env = gotool.SetEnv(os.Environ(), "STIPULATOR_LOCK_HOLDER", dir)
	input, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	child.Stderr = os.Stderr
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	released := false
	t.Cleanup(func() {
		if !released {
			input.Close()
			cancel()
			child.Wait()
		}
	})
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil || line != "held\n" {
		t.Fatalf("holder: %q %v", line, err)
	}
	loaded := make(chan []Record, 1)
	go func() { loaded <- Load(t.Context(), dir) }()
	select {
	case records := <-loaded:
		if len(records) != 1 {
			t.Fatal("contended cleanup prevented loading", records)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("opportunistic load queued behind the holder")
	}
	if _, err := os.Stat(orphan); err != nil {
		t.Fatal("contended load ran cleanup", err)
	}
	other := rec
	other.Test = "TestB"
	other.Outcomes = map[string]string{other.Key(): "passed"}
	other.CompartmentLedger = simpleLedger("TestB")
	for name, operation := range map[string]func(context.Context) error{
		"lock": func(ctx context.Context) error {
			lock, err := store.Lock(ctx)
			if lock != nil {
				lock.Close()
			}
			return err
		},
		"install": func(ctx context.Context) error { return Install(ctx, dir, other) },
		"gc": func(ctx context.Context) error {
			removed, kept, err := GC(ctx, dir, func(string, string) bool { return false }, nil)
			if removed != 0 || kept != 0 {
				return fmt.Errorf("cancelled queued collection did work: %d/%d", removed, kept)
			}
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			base, cancelWait := context.WithCancel(t.Context())
			defer cancelWait()
			wait := &lockWaitContext{Context: base, waiting: make(chan struct{})}
			done := make(chan error, 1)
			go func() { done <- operation(wait) }()
			select {
			case <-wait.waiting:
			case <-time.After(2 * time.Second):
				t.Fatal("operation never entered the contended wait")
			}
			cancelWait()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancelled waiter: %v", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("cancelled waiter requires the holder to release")
			}
		})
	}
	// Every waiter has returned while the holder still holds the lock.
	if _, err := os.Stat(filepath.Join(store.Path(), mustName(t, rec))); err != nil {
		t.Fatal("queued GC deleted its record", err)
	}
	if LoadLedger(dir, rec) == nil {
		t.Fatal("queued GC deleted its ledger")
	}
	input.Close()
	if err := child.Wait(); err != nil {
		t.Fatal(err)
	}
	released = true
	if records := Load(t.Context(), dir); len(records) != 1 || records[0].Test != rec.Test {
		t.Fatal("cancelled work resumed after unlock", records)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatalf("uncontended cleanup did not reclaim orphan: %v", err)
	}
}

func TestLedgerCollectionChecksCancellationBeforeDeletion(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-cache-format-ledger", "REQ-evidence-store-gc")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	rec := reusedLedgerRecord()
	if err := Install(t.Context(), dir, rec); err != nil {
		t.Fatal(err)
	}
	store, _ := open(dir)
	for _, collect := range []string{"load", "gc"} {
		t.Run(collect, func(t *testing.T) {
			path := filepath.Join(store.Path(), "ledgers", collect+"-orphan.json")
			if err := os.WriteFile(path, []byte("{}"), 0600); err != nil {
				t.Fatal(err)
			}
			past := time.Now().Add(-time.Hour)
			if err := os.Chtimes(path, past, past); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			beforeLedgerSweep = cancel
			t.Cleanup(func() { beforeLedgerSweep = nil })
			if collect == "load" {
				Load(ctx, dir)
			} else {
				_, _, err := GC(ctx, dir, func(string, string) bool { return true }, nil)
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("collection hid cancellation: %v", err)
				}
			}
			if _, err := os.Stat(path); err != nil {
				t.Fatal("collection deleted after cancellation", err)
			}
		})
	}
}

func TestRecordSweepChecksCancellationBeforeRemoval(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-store-gc")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	rec := reusedLedgerRecord()
	if err := Install(t.Context(), dir, rec); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	removed, _, err := GC(ctx, dir, func(string, string) bool { cancel(); return false }, nil)
	if !errors.Is(err, context.Canceled) || removed != 0 {
		t.Fatalf("cancelled before deletion: removed=%d err=%v", removed, err)
	}
	store, _ := recordstore.Open("witnesses", dir)
	if _, err := os.Stat(filepath.Join(store.Path(), mustName(t, rec))); err != nil {
		t.Fatal("record removed after cancellation", err)
	}
}

func TestLedgerCancellationRetainsCountsAfterRemovalFailure(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-store-gc")
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("requires Unix directory permissions enforced for this user")
	}
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	rec := reusedLedgerRecord()
	if err := Install(t.Context(), dir, rec); err != nil {
		t.Fatal(err)
	}
	store, err := open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(store.Path(), 0555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(store.Path(), 0755) })
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	beforeLedgerSweep = cancel
	t.Cleanup(func() { beforeLedgerSweep = nil })
	removed, kept, err := GC(ctx, dir, func(string, string) bool { return false }, nil)
	if !errors.Is(err, context.Canceled) || removed != 0 || kept != 0 {
		t.Fatalf("removal failure hid cancellation or invented completed work: %d/%d %v", removed, kept, err)
	}
	if _, err := os.Stat(filepath.Join(store.Path(), mustName(t, rec))); err != nil {
		t.Fatal("failed removal reported a vanished record", err)
	}
}
