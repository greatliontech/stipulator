//go:build linux

package resident

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
)

// procRoot is the process table the reading walks; tests point it at a
// synthetic tree.
var procRoot = "/proc"

// sample reads the process table once: the process's own status for its
// resident and peak bytes, every process's stat line for its parent and
// state, then the status of each live descendant for its resident and
// peak bytes. An unlistable table yields no reading; a descendant whose
// files vanish between the listing and the read has exited and is not
// counted; a descendant whose status exists but cannot be read voids
// the reading — a count missing a live descendant would be partial, and
// the datum is absent before it is partial; a stat line that does not
// parse names no process.
func sample() (Set, bool) { return sampleAt(procRoot, os.Getpid()) }

func sampleAt(root string, self int) (Set, bool) {
	status, err := os.ReadFile(filepath.Join(root, strconv.Itoa(self), "status"))
	if err != nil {
		return Set{}, false
	}
	rss, peak, ok := parseStatus(string(status))
	if !ok {
		return Set{}, false
	}
	set := Set{ProcessBytes: rss, ProcessPeakBytes: peak}
	entries, err := os.ReadDir(root)
	if err != nil {
		return Set{}, false
	}
	// The live children of every parent, from one pass over the table.
	children := map[int][]int{}
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid == self {
			continue
		}
		line, err := os.ReadFile(filepath.Join(root, entry.Name(), "stat"))
		if err != nil {
			continue
		}
		ppid, state, ok := parseStat(string(line))
		if !ok || !live(state) {
			continue
		}
		children[ppid] = append(children[ppid], pid)
	}
	// The descendants, breadth first; a process table has no cycles, and
	// the seen set guards the walk against a pid reused mid-listing.
	seen := map[int]bool{self: true}
	queue := []int{self}
	for len(queue) > 0 {
		parent := queue[0]
		queue = queue[1:]
		for _, pid := range children[parent] {
			if seen[pid] {
				continue
			}
			seen[pid] = true
			queue = append(queue, pid)
			text, err := os.ReadFile(filepath.Join(root, strconv.Itoa(pid), "status"))
			if exited(err) {
				continue
			}
			if err != nil {
				return Set{}, false
			}
			rss, peak, ok := parseStatus(string(text))
			if !ok {
				continue
			}
			set.Descendants++
			set.DescendantsBytes += rss
			set.DescendantPeakBytes = max(set.DescendantPeakBytes, peak)
		}
	}
	return set, true
}

// exited reports whether a /proc read failed because its process is
// gone: the entry vanished, or the kernel answers that no such process
// exists.
func exited(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ESRCH)
}
