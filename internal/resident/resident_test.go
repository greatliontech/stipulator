package resident

import (
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/greatliontech/stipulator/stipulate"
)

// TestStatusParsesTheTwoResidentLines pins the status reading over
// synthetic text: VmRSS and VmHWM in kibibytes become bytes, other lines
// are ignored, and a missing or malformed line yields no reading rather
// than a zero one (REQ-mcp-progress's resident datum is absent, never
// zero, where the host does not answer).
func TestStatusParsesTheTwoResidentLines(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-progress")
	rss, peak, ok := parseStatus("Name:\tstipulator\nVmPeak:\t 9999 kB\nVmHWM:\t  1004 kB\nVmRSS:\t   75 kB\nThreads:\t12\n")
	if !ok || rss != 75*1024 || peak != 1004*1024 {
		t.Fatalf("parseStatus = %d %d %v, want 76800 1028096 true", rss, peak, ok)
	}
	for name, text := range map[string]string{
		"no peak":       "VmRSS:\t 75 kB\n",
		"no rss":        "VmHWM:\t 75 kB\n",
		"wrong unit":    "VmHWM:\t 1 mB\nVmRSS:\t 75 kB\n",
		"not a number":  "VmHWM:\t x kB\nVmRSS:\t 75 kB\n",
		"empty":         "",
		"missing value": "VmHWM:\nVmRSS:\t 75 kB\n",
	} {
		if _, _, ok := parseStatus(text); ok {
			t.Errorf("%s: parsed a reading from %q", name, text)
		}
	}
}

// TestStatParsesPastTheCommandName pins the stat reading: the fields
// after the parenthesized command name, which may itself carry spaces
// and parentheses, give the state and the parent pid; a short or
// malformed line yields no reading; a zombie or dead state is not live.
func TestStatParsesPastTheCommandName(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-progress")
	for _, comm := range []string{"(stipulator)", "(go test (race) x)", "(a) b)"} {
		ppid, state, ok := parseStat("1234 " + comm + " S 4321 1 1 0 -1 4194560 100 0")
		if !ok || ppid != 4321 || state != 'S' {
			t.Errorf("%q: parseStat = %d %c %v, want 4321 S true", comm, ppid, state, ok)
		}
	}
	for name, line := range map[string]string{
		"no paren":  "1234 stipulator S 4321",
		"short":     "1234 (x) S",
		"bad ppid":  "1234 (x) S x 1 1",
		"bad state": "1234 (x) SS 4321 1",
	} {
		if _, _, ok := parseStat(line); ok {
			t.Errorf("%s: parsed a reading from %q", name, line)
		}
	}
	for state, want := range map[byte]bool{'S': true, 'R': true, 'D': true, 'T': true, 'Z': false, 'X': false} {
		if live(state) != want {
			t.Errorf("live(%c) = %v, want %v", state, !want, want)
		}
	}
}

// procTree writes a synthetic process table: pid → (ppid, state, rss kB,
// hwm kB); a pid with a negative rss has no status file (exited between
// the listing and the read).
func procTree(t *testing.T, procs map[int][4]int) string {
	t.Helper()
	root := t.TempDir()
	for pid, p := range procs {
		dir := filepath.Join(root, strconv.Itoa(pid))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		stat := strconv.Itoa(pid) + " (p" + strconv.Itoa(pid) + ") " + string(rune(p[1])) + " " + strconv.Itoa(p[0]) + " 1 1 0 -1 4194560 100 0\n"
		if err := os.WriteFile(filepath.Join(dir, "stat"), []byte(stat), 0o644); err != nil {
			t.Fatal(err)
		}
		if p[2] < 0 {
			continue
		}
		status := "Name:\tp" + strconv.Itoa(pid) + "\nVmHWM:\t" + strconv.Itoa(p[3]) + " kB\nVmRSS:\t" + strconv.Itoa(p[2]) + " kB\n"
		if err := os.WriteFile(filepath.Join(dir, "status"), []byte(status), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// TestSampleWalksTheLiveDescendants pins the walk over a synthetic
// table (REQ-mcp-progress): every process whose parent chain reaches the
// sampled one counts — the go driver, the package test binary beneath
// it, the resolver child — summed by resident bytes with the largest
// single descendant's own peak; a zombie, a dead process, a sibling, an
// unrelated process, and a descendant whose status vanished are not
// counted; an unlistable table or an unreadable own status yields no
// reading rather than a zero one.
func TestSampleWalksTheLiveDescendants(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-progress")
	root := procTree(t, map[int][4]int{
		1:   {0, 'S', 1, 1},        // init
		100: {1, 'S', 75, 1004},    // self
		101: {1, 'S', 500, 500},    // a sibling: not a descendant
		200: {100, 'S', 400, 505},  // the resolver child
		201: {100, 'S', 10, 12},    // a go driver
		300: {201, 'R', 900, 950},  // the package test binary beneath it
		301: {201, 'Z', 0, 0},      // a reaped-pending zombie: not live
		302: {300, 'X', 0, 0},      // dead: not live
		303: {300, 'S', -1, 0},     // exited between the listing and the read
		400: {101, 'S', 999, 9999}, // the sibling's child: not ours
	})
	set, trees, ok := sampleAt(root, 100)
	want := Set{ProcessBytes: 75 * 1024, ProcessPeakBytes: 1004 * 1024, Descendants: 3, DescendantsBytes: (400 + 10 + 900) * 1024, DescendantPeakBytes: 950 * 1024}
	if !ok || set != want {
		t.Fatalf("sampleAt = %+v %v, want %+v true", set, ok, want)
	}
	// Each direct child's tree is attributed whole: the resolver child
	// alone, the go driver with the test binary beneath it.
	wantTrees := map[int]uint64{200: 400 * 1024, 201: (10 + 900) * 1024}
	if !maps.Equal(trees, wantTrees) {
		t.Fatalf("sampleAt trees = %v, want %v", trees, wantTrees)
	}
	if set, _, ok := sampleAt(root, 999); ok || set != (Set{}) {
		t.Fatalf("a process without a status answered %+v %v", set, ok)
	}
	if set, _, ok := sampleAt(filepath.Join(root, "missing"), 100); ok || set != (Set{}) {
		t.Fatalf("an unlistable table answered %+v %v", set, ok)
	}
	// A descendant whose status exists but cannot be read voids the
	// reading: a count missing it would be partial.
	if os.Getuid() != 0 {
		guarded := procTree(t, map[int][4]int{100: {1, 'S', 75, 1004}, 200: {100, 'S', 400, 505}, 201: {100, 'S', 10, 12}})
		if err := os.Chmod(filepath.Join(guarded, "201", "status"), 0o000); err != nil {
			t.Fatal(err)
		}
		if set, _, ok := sampleAt(guarded, 100); ok {
			t.Fatalf("an unreadable descendant status answered a partial %+v", set)
		}
	}
	// The own status readable but the table unlistable — a table that can
	// be traversed but not listed: no reading, never "no descendants".
	lone := t.TempDir()
	if err := os.MkdirAll(filepath.Join(lone, "100"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lone, "100", "status"), []byte("VmHWM:\t2 kB\nVmRSS:\t1 kB\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(lone, 0o111); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(lone, 0o755) })
	if os.Getuid() != 0 {
		if _, err := os.ReadFile(filepath.Join(lone, "100", "status")); err != nil {
			t.Fatalf("the fixture's own status must stay readable through the unlistable table: %v", err)
		}
		if set, _, ok := sampleAt(lone, 100); ok {
			t.Fatalf("an unlistable table beside a readable status answered %+v", set)
		}
	}
}

// TestSampleCountsALiveDescendantChain pins the live reading on the host
// that answers it: the process's own set is positive and bounded by its
// peak, and a shell this process spawned with the sleep beneath it are
// both counted, with resident bytes, until they are reaped.
func TestSampleCountsALiveDescendantChain(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-progress")
	if runtime.GOOS != "linux" {
		if _, ok := Sample(); ok {
			t.Fatal("a host without the /proc reading answered a sample")
		}
		t.Skip("the reading has a Linux form only")
	}
	before, ok := Sample()
	if !ok || before.ProcessBytes == 0 || before.ProcessPeakBytes < before.ProcessBytes {
		t.Fatalf("own sample = %+v %v, want a positive set under its peak", before, ok)
	}
	// The trailing command keeps the shell alive beside its sleep: two
	// descendants, one of them a grandchild.
	child := exec.Command("sh", "-c", "sleep 30; true")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = child.Process.Kill()
		_ = child.Wait()
	}()
	var during Set
	for range 50 { // the shell forks its sleep shortly after starting
		during, ok = Sample()
		if ok && during.Descendants >= before.Descendants+2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !ok || during.Descendants < before.Descendants+2 || during.DescendantsBytes <= before.DescendantsBytes || during.DescendantPeakBytes == 0 {
		t.Fatalf("sample beside a live shell and its sleep = %+v (before %+v), want two more descendants with resident bytes and a peak", during, before)
	}
	_ = child.Process.Kill()
	_ = child.Wait()
	after, _ := Sample()
	if after.Descendants > before.Descendants+1 {
		// The orphaned sleep may be re-parented to init rather than to
		// this process; a reaped shell is never counted.
		t.Fatalf("reaped shell still counted: %d descendants, before %d", after.Descendants, before.Descendants)
	}
}
