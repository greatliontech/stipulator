package golang

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// retainedFlags is cmd/go's test-flag projection, not policy validation. In
// particular test.foo and foo share a value but remain distinct visited flags.
// Raw binary arguments never enter this parser: flag.Parse's positional stop,
// unknown flags, and last-assignment behavior belong to the binary itself.
type retainedFlags struct {
	values    map[string]string
	visited   map[string]bool
	cover     bool
	coverMode string
	coverPkg  string
	keepWork  bool
	output    string
}

// The kinds match cmd/go/internal/test/testflag.go, including its string-valued
// numeric flags. Validation of their binary values happens in testing, not here.
var retainedFlagKinds = map[string]string{
	"artifacts": "bool", "bench": "string", "benchmem": "bool", "benchtime": "string",
	"blockprofile": "string", "blockprofilerate": "string", "count": "int",
	"coverprofile": "string", "cpu": "string", "cpuprofile": "string", "failfast": "bool",
	"fullpath": "bool", "fuzz": "string", "fuzzminimizetime": "string", "fuzztime": "string",
	"list": "string", "memprofile": "string", "memprofilerate": "string", "mutexprofile": "string",
	"mutexprofilefraction": "string", "outputdir": "path", "parallel": "int", "run": "string",
	"short": "bool", "shuffle": "shuffle", "skip": "string", "timeout": "duration", "trace": "string", "v": "verbose",
}

// splitRetainedGOFLAGS follows cmd/internal/quoted: quotes only at the start of
// a word, no escapes, ASCII whitespace, and adjacent quoted words stay distinct.
func splitRetainedGOFLAGS(s string) ([]string, error) {
	var words []string
	for {
		s = strings.TrimLeft(s, " \t\r\n")
		if s == "" {
			return words, nil
		}
		if s[0] == '\'' || s[0] == '"' {
			quote := s[0]
			s = s[1:]
			i := strings.IndexByte(s, quote)
			if i < 0 {
				return nil, fmt.Errorf("unterminated %c string in GOFLAGS", quote)
			}
			words = append(words, s[:i])
			s = s[i+1:]
		} else {
			i := strings.IndexAny(s, " \t\r\n")
			if i < 0 {
				return append(words, s), nil
			}
			words = append(words, s[:i])
			s = s[i:]
		}
	}
}

func retainedGOFLAGS(n *NormalizedInvocation) (retainedFlags, error) {
	f := retainedFlags{values: map[string]string{}, visited: map[string]bool{}}
	words, err := splitRetainedGOFLAGS(n.GOFLAGS)
	if err != nil {
		return f, err
	}
	for _, word := range words {
		name, value, hasValue := strings.Cut(strings.TrimPrefix(strings.TrimPrefix(word, "-"), "-"), "=")
		short := strings.TrimPrefix(name, "test.")
		kind, ok := retainedFlagKinds[short]
		if !ok {
			switch name {
			case "cover":
				if !hasValue {
					value = "true"
				}
				f.cover, err = strconv.ParseBool(value)
			case "covermode":
				f.coverMode = value
				f.cover = true
			case "coverpkg":
				f.coverPkg = value
				f.cover = true
			case "o":
				f.output = value
			case "work":
				if !hasValue {
					value = "true"
				}
				f.keepWork, err = strconv.ParseBool(value)
			}
			if err != nil {
				return f, err
			}
			continue // Build flags remain in the compiler's complete GOFLAGS.
		}
		if !hasValue {
			if kind != "bool" && kind != "verbose" {
				return f, fmt.Errorf("GOFLAGS -%s needs a value", name)
			}
			value = "true"
		}
		switch kind {
		case "bool", "verbose":
			if kind == "verbose" && value == "test2json" {
				break
			}
			var b bool
			b, err = strconv.ParseBool(value)
			value = strconv.FormatBool(b)
		case "int":
			var i int64
			i, err = strconv.ParseInt(value, 0, strconv.IntSize)
			value = strconv.FormatInt(i, 10)
		case "duration":
			var d time.Duration
			d, err = time.ParseDuration(value)
			value = d.String()
		case "shuffle":
			if value != "on" && value != "off" {
				var i int64
				i, err = strconv.ParseInt(value, 10, 64)
				value = strconv.FormatInt(i, 10)
			}
		case "path":
			if value != "" {
				if !filepath.IsAbs(value) {
					value = filepath.Join(n.Dir, value)
				} else {
					value = filepath.Clean(value)
				}
			}
		}
		if err != nil {
			return f, fmt.Errorf("GOFLAGS -%s: %w", name, err)
		}
		f.values[short], f.visited[name] = value, true
		// cmd/go's coverFlag.Set enables instrumentation at assignment time,
		// even for an empty value. A later -cover=false can turn it off.
		if short == "coverprofile" {
			f.cover = true
		}
	}
	if f.coverMode == "" {
		f.coverMode = "set"
		if n.Race {
			f.coverMode = "atomic"
		}
	}
	return f, nil
}

func (f retainedFlags) outputDir(n *NormalizedInvocation) string {
	if d := f.values["outputdir"]; d != "" {
		return d
	}
	return n.Dir
}

func (f retainedFlags) runtimeArgs(n *NormalizedInvocation, selection []string, logPath string) []string {
	args := []string{"-test.paniconexit0", "-test.v=test2json"}
	names := make([]string, 0, len(f.visited))
	for name := range f.visited {
		names = append(names, name)
	}
	sort.Strings(names) // FlagSet.Visit's ordering, including shared aliases.
	for _, name := range names {
		short := strings.TrimPrefix(name, "test.")
		if short == "v" || short == "timeout" || short == "count" && (n.CacheBypass || n.Count > 0) || short == "run" && selection != nil {
			continue
		}
		args = append(args, "-test."+short+"="+f.values[short])
	}
	needsOutput := f.values["artifacts"] == "true"
	for _, name := range []string{"blockprofile", "cpuprofile", "memprofile", "mutexprofile", "trace"} {
		needsOutput = needsOutput || f.values[name] != ""
	}
	if needsOutput && !f.visited["outputdir"] && !f.visited["test.outputdir"] {
		args = append(args, "-test.outputdir="+f.outputDir(n))
	}
	args = append(args, "-test.timeout=0s")
	if n.CacheBypass {
		args = append(args, "-test.count=1")
	} else if n.Count > 0 {
		args = append(args, fmt.Sprintf("-test.count=%d", n.Count))
	}
	if selection != nil {
		quoted := make([]string, len(selection))
		for i, name := range selection {
			quoted[i] = regexp.QuoteMeta(name)
		}
		args = append(args, "-test.run=^("+strings.Join(quoted, "|")+")$")
	}
	if logPath != "" {
		args = append(args, "-test.testlogfile="+logPath)
	}
	return append(args, n.Args...)
}
