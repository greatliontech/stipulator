//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package recordstore

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/greatliontech/gofresh/gotool"
	"github.com/greatliontech/stipulator/stipulate"
	"golang.org/x/sys/unix"
)

func TestStoreLockExcludesOtherProcesses(t *testing.T) {
	if dir := os.Getenv("STIPULATOR_STORE_LOCK"); dir != "" {
		store, err := Open("witnesses", dir)
		if err != nil {
			t.Fatal(err)
		}
		f, err := os.OpenFile(store.Path()+".lock", os.O_RDWR, 0)
		if err != nil {
			t.Fatal(err)
		}
		err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		f.Close()
		if !errors.Is(err, unix.EWOULDBLOCK) {
			t.Fatalf("parent holds no exclusive lock: %v", err)
		}
		fmt.Println("parent lock held")
		lock, err := store.Lock(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		lock.Close()
		return
	}
	stipulate.Covers(t, "REQ-evidence-witness-cache-format-ledger")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	store, err := Open("witnesses", dir)
	if err != nil {
		t.Fatal(err)
	}
	lock, err := store.Lock(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestStoreLockExcludesOtherProcesses$", "-test.count=1")
	child.Env = gotool.SetEnv(os.Environ(), "STIPULATOR_STORE_LOCK", dir)
	out, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	child.Stderr = os.Stderr
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	scan := bufio.NewScanner(out)
	if !scan.Scan() || scan.Text() != "parent lock held" {
		cancel()
		child.Wait()
		t.Fatal("child did not observe exclusion", scan.Text(), scan.Err())
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	for scan.Scan() {
	}
	if err := child.Wait(); err != nil {
		t.Fatal(err)
	}
}
