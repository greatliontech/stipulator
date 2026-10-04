// Package resident reads a process's resident set: its resident and peak
// resident bytes, and its live descendants — the resolver child, the go
// drivers and the package test binaries beneath them — as their count,
// their summed resident bytes, and the largest single descendant's own
// peak. It is the material of the progress stream's resident datum
// (REQ-mcp-progress): a reading the host answers, on Linux through
// /proc; a host that does not answer, or a /proc that cannot be listed,
// yields no sample, and the datum is absent rather than zero. A reading
// walks the whole process table once; it is taken at phase transitions
// and endings only, so its cost is bounded by those.
package resident

import (
	"strconv"
	"strings"
)

// Set is one reading of a process's memory.
type Set struct {
	// ProcessBytes is the process's resident set at the reading.
	ProcessBytes uint64
	// ProcessPeakBytes is the process's peak resident set as the kernel
	// answers it — the larger of its stored high-water mark and the
	// current set, so two readings need not be monotonic; a reader
	// wanting a monotonic peak keeps its own maximum.
	ProcessPeakBytes uint64
	// Descendants counts the process's live descendants — every process
	// whose parent chain reaches it, zombies and dead processes
	// excluded; DescendantsBytes sums their resident sets at the
	// reading, and DescendantPeakBytes is the largest single
	// descendant's own peak resident set.
	Descendants         int
	DescendantsBytes    uint64
	DescendantPeakBytes uint64
}

// Sample reads the running process's set. ok is false where the host
// does not answer — not Linux, or /proc unreadable — and the Set is then
// the zero value, which no reader renders.
func Sample() (Set, bool) { return sample() }

// parseStatus reads the resident and peak resident bytes from a
// /proc/<pid>/status text, whose VmRSS and VmHWM lines carry kibibytes.
// ok is false unless both lines parse.
func parseStatus(text string) (rss, peak uint64, ok bool) {
	var haveRSS, havePeak bool
	for _, line := range strings.Split(text, "\n") {
		key, rest, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		switch key {
		case "VmRSS", "VmHWM":
		default:
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) != 2 || fields[1] != "kB" {
			return 0, 0, false
		}
		n, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			return 0, 0, false
		}
		if key == "VmRSS" {
			rss, haveRSS = n*1024, true
		} else {
			peak, havePeak = n*1024, true
		}
	}
	return rss, peak, haveRSS && havePeak
}

// parseStat reads the parent pid and the state from a /proc/<pid>/stat
// line. The second field, the command name, is parenthesized and may
// itself hold spaces and parentheses, so the fields after it are split
// from the last closing parenthesis: the state is the first of those,
// the parent pid the second. ok is false for any other shape.
func parseStat(line string) (ppid int, state byte, ok bool) {
	i := strings.LastIndexByte(line, ')')
	if i < 0 {
		return 0, 0, false
	}
	fields := strings.Fields(line[i+1:])
	if len(fields) < 2 || len(fields[0]) != 1 {
		return 0, 0, false
	}
	p, err := strconv.Atoi(fields[1])
	if err != nil {
		return 0, 0, false
	}
	return p, fields[0][0], true
}

// live reports whether a process in state is a live descendant: a zombie
// (Z) holds no memory and awaits its reaping, a dead process (X) is
// gone; every other state is live.
func live(state byte) bool { return state != 'Z' && state != 'X' }
