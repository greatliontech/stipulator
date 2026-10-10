package golang

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/guard"
	"github.com/greatliontech/stipulator/internal/recordstore"
	"github.com/greatliontech/stipulator/internal/witnesscache"
	"github.com/greatliontech/stipulator/stipulate"
)

type publicationWaitContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (c *publicationWaitContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

func TestPublicationPassesCancellationToStoreAcquisition(t *testing.T) {
	stipulate.Covers(t, "REQ-policy-cancellation", "REQ-evidence-witness-cache-format-ledger")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	store, err := recordstore.Open("witnesses", dir)
	if err != nil {
		t.Fatal(err)
	}
	lock, err := store.Lock(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	rec := publicationRecord("TestA")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	wait := &publicationWaitContext{Context: ctx, waiting: make(chan struct{})}
	reasons := map[gofresh.Subject]uncacheable{}
	type result struct {
		installed []witnesscache.Record
		err       error
	}
	done := make(chan result, 1)
	go func() {
		installed, err := installRecords(wait, dir, []witnesscache.Record{rec}, reasons)
		done <- result{installed, err}
	}()
	select {
	case <-wait.waiting:
	case <-time.After(2 * time.Second):
		t.Fatal("publication did not wait on the caller's context")
	}
	cancel()
	select {
	case got := <-done:
		if !errors.Is(got.err, context.Canceled) || len(got.installed) != 0 || len(reasons) != 0 {
			t.Fatalf("cancellation treated as a cache fault: %+v reasons=%v", got, reasons)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("publication stayed blocked after cancellation")
	}
	lock.Close()
	if got := witnesscache.Load(t.Context(), dir); len(got) != 0 {
		t.Fatal("cancelled publication landed a record", got)
	}
}

func publicationRecord(test string) witnesscache.Record {
	return witnesscache.Record{Group: "group", Package: "example.com/p", Test: test, Outcomes: map[string]string{"example.com/p." + test: "passed"}, Fingerprint: gofresh.Fingerprint{
		MaximalClosure: strings.Repeat("a", 32), TestVariantClosure: strings.Repeat("b", 32), ClosureStrategy: gofresh.ClosureStrategy, DynamicStateStrategy: gofresh.DynamicStateStrategy,
		Guards: guard.Guards{Toolchain: "go1.27.2", BuildConfig: strings.Repeat("c", 32)}, RuntimeInputs: "eyJ2IjoyfQ", RuntimeDigest: "3a79bf37b571938d1f2907afb6a643f4", ResultKind: gofresh.CodeResult,
	}}
}

type installedContext struct {
	context.Context
	path   string
	cancel context.CancelFunc
}

func (c *installedContext) Err() error {
	if _, err := os.Stat(c.path); err == nil {
		c.cancel()
	}
	return c.Context.Err()
}

func TestPublicationCancellationRetainsTheInstalledPrefix(t *testing.T) {
	stipulate.Covers(t, "REQ-policy-cancellation-kept")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	first, second := publicationRecord("TestFirst"), publicationRecord("TestSecond")
	store, err := recordstore.Open("witnesses", dir)
	if err != nil {
		t.Fatal(err)
	}
	name, err := recordstore.Name([]string{first.Group, first.Package, first.Test}, first.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	cut := &installedContext{Context: ctx, path: filepath.Join(store.Path(), name), cancel: cancel}
	reasons := map[gofresh.Subject]uncacheable{}
	installed, err := installRecords(cut, dir, []witnesscache.Record{first, second}, reasons)
	if !errors.Is(err, context.Canceled) || len(installed) != 1 || installed[0].Test != "TestFirst" || len(reasons) != 0 {
		t.Fatalf("installed prefix lost: records=%v reasons=%v err=%v", installed, reasons, err)
	}
	if got := witnesscache.Load(t.Context(), dir); len(got) != 1 || got[0].Test != "TestFirst" {
		t.Fatal("returned prefix disagrees with the store", got)
	}
}
