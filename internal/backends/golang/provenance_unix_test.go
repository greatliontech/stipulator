//go:build unix

package golang

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/greatliontech/stipulator/stipulate"
)

// A cancelled probe returns within its wait bound even when a shim on
// PATH left a descendant holding the output pipe: the kill reaches
// the process alone, and the bounded wait keeps the cancellation live.
func TestCancelledProbeReturnsWithinItsWaitBound(t *testing.T) {
	stipulate.Covers(t, "REQ-policy-cancellation")
	bin := t.TempDir()
	shim := filepath.Join(bin, "go")
	if err := os.WriteFile(shim, []byte("#!/bin/sh\n/bin/sleep 8 &\nwait\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	_, err := sampleGoVersion(ctx, ".", nil)
	if err == nil {
		t.Fatal("a cancelled probe reported a version")
	}
	if took := time.Since(start); took > probeWaitDelay+2*time.Second {
		t.Fatalf("cancelled probe held its caller %v; want within the wait bound", took)
	}
}

// A wrapper that answers, exits, and leaves a helper holding the pipe
// has sampled: the answer — the first line, whatever the helper writes
// after it — is taken after the bounded wait, never refused (and never
// memoized as a refusal) over the wrapper's housekeeping.
func TestProbeTakesTheAnswerAWrapperLeftBehind(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-toolchain-provenance")
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "go"), []byte("#!/bin/sh\necho go1.27.0\n( /bin/sleep 1; echo helper-done; /bin/sleep 4 ) &\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	start := time.Now()
	v, err := sampleGoVersion(context.Background(), ".", nil)
	if err != nil || v != "go1.27.0" {
		t.Fatalf("sample behind a lingering helper = %q, %v; want the wrapper's answer", v, err)
	}
	if took := time.Since(start); took > probeWaitDelay+2*time.Second {
		t.Fatalf("sample held its caller %v; want within the wait bound", took)
	}
}
