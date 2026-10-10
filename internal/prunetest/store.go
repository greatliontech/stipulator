// Package prunetest builds real cache files for store-prune interruption tests.
package prunetest

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/guard"
	"github.com/greatliontech/stipulator/internal/backends/golang"
	"github.com/greatliontech/stipulator/internal/recordstore"
	"github.com/greatliontech/stipulator/internal/resolutioncache"
	"github.com/greatliontech/stipulator/internal/witnesscache"
)

// Store is a collection whose first deletion triggers cancellation. Entries
// after that deletion remain unexamined; explicit mtimes establish scan order.
type Store struct {
	Root         string
	FirstDeleted string
	Unexamined   string
	resolution   bool
}

// New creates either a partial witness collection or a completed witness
// collection followed by a partial resolution collection.
func New(t *testing.T, resolution bool) Store {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	f := Store{Root: t.TempDir(), resolution: resolution}
	fp := gofresh.Fingerprint{
		MaximalClosure: strings.Repeat("a", 32), TestVariantClosure: strings.Repeat("b", 32),
		ClosureStrategy: gofresh.ClosureStrategy, DynamicStateStrategy: gofresh.DynamicStateStrategy,
		Guards:        guard.Guards{Toolchain: "go1.27.2", BuildConfig: strings.Repeat("c", 32)},
		RuntimeInputs: "eyJ2IjoyfQ", RuntimeDigest: "3a79bf37b571938d1f2907afb6a643f4", ResultKind: gofresh.CodeResult,
	}
	const pkg = "example.com/prunegc"
	var bindings string
	role := "BINDING_ROLE_TESTS"
	if resolution {
		role = "BINDING_ROLE_IMPLEMENTS"
	}
	for _, name := range []string{"KeepA", "KeepB"} {
		bindings += fmt.Sprintf("bindings { requirement_id: \"REQ-gc-a\" backend: \"go\" symbol: %q role: %s }\n", pkg+"."+name, role)
	}
	bindingDir := filepath.Join(f.Root, ".stipulator", "bindings")
	if err := os.MkdirAll(bindingDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bindingDir, "gc.textproto"), []byte(bindings), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.Root, ".stipulator", "manifest.textproto"), []byte("include: \"specs/**/*.md\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	witnesses := []string{"KeepA", "KeepB", "Delete", "Unexamined"}
	if resolution {
		witnesses = []string{"WitnessA", "WitnessB", "WitnessC"}
	}
	for i, name := range witnesses {
		rec := witnesscache.Record{Group: "group", Package: pkg, Test: name, Fingerprint: fp, Outcomes: map[string]string{pkg + "." + name: "passed"}}
		if err := witnesscache.Install(t.Context(), f.Root, rec); err != nil {
			t.Fatal(err)
		}
		store, err := witnesscache.StoreDir(f.Root)
		if err != nil {
			t.Fatal(err)
		}
		file, err := recordstore.Name([]string{rec.Group, rec.Package, rec.Test}, rec.Fingerprint)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(store, file)
		stamp(t, path, i)
		if name == "Delete" {
			f.FirstDeleted = path
		}
		if name == "Unexamined" {
			f.Unexamined = path
		}
	}
	if resolution {
		for i, name := range []string{"KeepA", "KeepB", "Delete", "Unexamined"} {
			rec := resolutioncache.Record{Selection: "default", Symbol: pkg + "." + name, Fingerprint: resolutioncache.SourceTiers(fp), Resolution: "resolved", Package: pkg}
			if err := resolutioncache.InstallAll(f.Root, []resolutioncache.Record{rec}); err != nil {
				t.Fatal(err)
			}
			store, err := resolutioncache.StoreDir(f.Root)
			if err != nil {
				t.Fatal(err)
			}
			file, err := recordstore.Name([]string{rec.Selection, rec.Symbol}, rec.Fingerprint)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(store, file)
			stamp(t, path, i)
			if name == "Delete" {
				f.FirstDeleted = path
			}
			if name == "Unexamined" {
				f.Unexamined = path
			}
		}
	}
	return f
}

func stamp(t *testing.T, path string, index int) {
	t.Helper()
	at := time.Unix(1_700_000_000-int64(index), 0)
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}

// Capture supplies empty witness discovery for the resolution case: no witness
// group is live, while the bound resolution symbols remain live.
func (f Store) Capture(context.Context) (*golang.Capture, error) {
	if f.resolution {
		return &golang.Capture{}, nil
	}
	return nil, errors.New("no captured policy")
}

type afterDeletion struct {
	context.Context
	cancel context.CancelFunc
	path   string
}

func (c *afterDeletion) Err() error {
	if _, err := os.Stat(c.path); os.IsNotExist(err) {
		c.cancel()
	}
	return c.Context.Err()
}

// Context cancels at the next cancellation check after the first actual
// deletion. It uses no clocks or scheduler-dependent filesystem polling.
func (f Store) Context(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	return &afterDeletion{Context: ctx, cancel: cancel, path: f.FirstDeleted}, cancel
}

// CheckPrefix verifies the destructive work and the untouched remainder.
func (f Store) CheckPrefix(t *testing.T) {
	t.Helper()
	if _, err := os.Stat(f.FirstDeleted); !os.IsNotExist(err) {
		t.Fatalf("first deletion did not happen: %v", err)
	}
	if _, err := os.Stat(f.Unexamined); err != nil {
		t.Fatalf("unexamined record did not survive: %v", err)
	}
}
