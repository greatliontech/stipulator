package witnesscache

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/greatliontech/gofresh/gotool"
	"github.com/greatliontech/stipulator/stipulate"
)

func reusedLedgerRecord() Record {
	return Record{Group: "group", Package: "example.com/p", Test: "TestA", Fingerprint: ledgerFingerprint(strings.Repeat("e", 32)), CompartmentLedger: simpleLedger("TestA"), Outcomes: map[string]string{"example.com/p.TestA": "passed"}}
}

// The installer starts in another process after the collector's final snapshot.
// Its old ledger already exists. Exclusion keeps it from reusing that file
// until reclamation finishes; afterwards installation recreates the pair.
func TestCollectorsSerializeOldLedgerReuseAcrossProcesses(t *testing.T) {
	if dir := os.Getenv("STIPULATOR_LEDGER_INSTALL"); dir != "" {
		fmt.Println("attempting install")
		if err := Install(t.Context(), dir, reusedLedgerRecord()); err != nil {
			t.Fatal(err)
		}
		return
	}
	stipulate.Covers(t, "REQ-evidence-witness-cache-format-ledger", "REQ-evidence-store-gc")
	for _, collect := range []string{"load", "gc"} {
		t.Run(collect, func(t *testing.T) {
			t.Setenv("XDG_CACHE_HOME", t.TempDir())
			dir := t.TempDir()
			rec := reusedLedgerRecord()
			if err := Install(t.Context(), dir, rec); err != nil {
				t.Fatal(err)
			}
			store, err := StoreDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(filepath.Join(store, mustName(t, rec))); err != nil {
				t.Fatal(err)
			}
			path := ledgerPath(store, coordinateOf(rec).key())
			past := time.Now().Add(-time.Hour)
			if err := os.Chtimes(path, past, past); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCollectorsSerializeOldLedgerReuseAcrossProcesses$", "-test.count=1")
			child.Env = gotool.SetEnv(os.Environ(), "STIPULATOR_LEDGER_INSTALL", dir)
			out, err := child.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			child.Stderr = os.Stderr
			var done chan error
			beforeLedgerSweep = func() {
				if err := child.Start(); err != nil {
					t.Fatal(err)
				}
				scan := bufio.NewScanner(out)
				if !scan.Scan() || scan.Text() != "attempting install" {
					t.Fatal("installer did not reach install", scan.Text(), scan.Err())
				}
				done = make(chan error, 1)
				go func() {
					for scan.Scan() {
					}
					done <- child.Wait()
				}()
				select {
				case err := <-done:
					t.Errorf("installer crossed collector's protected interval: %v", err)
					done <- err
				case <-time.After(500 * time.Millisecond):
					// The collector still holds the lock. Return to let it delete
					// the orphan and unlock, then require the installer to finish.
				}
			}
			t.Cleanup(func() { beforeLedgerSweep = nil })
			if collect == "load" {
				Load(t.Context(), dir)
			} else {
				if _, _, err := GC(t.Context(), dir, func(string, string) bool { return true }, nil); err != nil {
					t.Fatal(err)
				}
			}
			beforeLedgerSweep = nil
			if done == nil {
				t.Fatal("collector did not reach protected sweep")
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal("installer stayed blocked after collection", ctx.Err())
			}
			if got := LoadLedger(dir, rec); got == nil {
				t.Fatal("collector removed the concurrently reused ledger")
			}
			if got := Load(t.Context(), dir); len(got) != 1 {
				t.Fatalf("installer's record lost: %v", got)
			}
		})
	}
}
