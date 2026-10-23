package golang

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/greatliontech/gofresh"
)

// nativeRunnableSubjects reads the arguments the retained binary will actually
// parse, after GOFLAGS, typed selection and raw-argument precedence. An unknown
// custom flag's arity cannot be guessed: execution proceeds, but no native
// transaction may assert a roster derived by reading through its value.
// names must come from the producing snapshot's validated runnable generation.
func nativeRunnableSubjects(names []string, pkg string, args []string) ([]gofresh.Subject, error) {
	values := map[string]string{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" || arg == "-" || !strings.HasPrefix(arg, "-") {
			break
		}
		name, value, equal := strings.Cut(strings.TrimPrefix(strings.TrimPrefix(arg, "-"), "-"), "=")
		short, standard := strings.CutPrefix(name, "test.")
		kind, known := retainedFlagKinds[short]
		switch short {
		case "paniconexit0":
			kind, known = "bool", true
		case "testlogfile", "gocoverdir", "fuzzcachedir":
			kind, known = "string", true
		}
		if !standard || !known {
			return nil, fmt.Errorf("native runnable selection cannot determine the arity of binary flag %q", name)
		}
		if !equal {
			if kind == "bool" || kind == "verbose" {
				value = "true"
			} else {
				i++
				if i == len(args) {
					return nil, fmt.Errorf("native runnable selection: flag %q has no value", name)
				}
				value = args[i]
			}
		}
		values[short] = value
	}
	for _, name := range []string{"list", "bench", "fuzz"} {
		if values[name] != "" {
			return nil, fmt.Errorf("native runnable selection cannot attest a test-package roster under -test.%s", name)
		}
	}
	run, err := nativeTopPattern(values["run"])
	if err != nil {
		return nil, err
	}
	var skip []nativePatternBranch
	if values["skip"] != "" {
		skip, err = nativeTopPattern(values["skip"])
		if err != nil {
			return nil, err
		}
	}
	var subjects []gofresh.Subject
	for _, name := range names {
		matched, _ := nativeTopMatch(run, name)
		skipped, partial := nativeTopMatch(skip, name)
		if matched && (!skipped || partial) {
			subjects = append(subjects, gofresh.Subject{Package: pkg, Symbol: name})
		}
	}
	return subjects, nil
}

type nativePatternBranch struct {
	first   *regexp.Regexp
	partial bool
}

// Go's test matcher splits slash segments and alternatives only outside grouped
// expressions and character classes. A top-level runnable is admitted on a
// partial run match; a partial skip match must still enter its parent. Alternative
// order matters because the first matching branch determines partiality.
func nativeTopPattern(pattern string) ([]nativePatternBranch, error) {
	var branches []nativePatternBranch
	var segments []string
	start, classes, groups := 0, 0, 0
	finish := func(end int) error {
		segments = append(segments, pattern[start:end])
		branch := nativePatternBranch{partial: len(segments) > 1}
		for i, segment := range segments {
			var rewritten strings.Builder
			for _, r := range segment {
				switch {
				case unicode.IsSpace(r):
					rewritten.WriteByte('_')
				case !strconv.IsPrint(r):
					quoted := strconv.QuoteRune(r)
					rewritten.WriteString(quoted[1 : len(quoted)-1])
				default:
					rewritten.WriteRune(r)
				}
			}
			re, err := regexp.Compile(rewritten.String())
			if err != nil {
				return fmt.Errorf("native runnable selector %q: %w", pattern, err)
			}
			if i == 0 {
				branch.first = re
			}
		}
		branches = append(branches, branch)
		segments = nil
		return nil
	}
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '\\':
			i++
		case '[':
			classes++
		case ']':
			if classes > 0 {
				classes--
			}
		case '(':
			if classes == 0 {
				groups++
			}
		case ')':
			if classes == 0 {
				groups--
			}
		case '/':
			if classes == 0 && groups == 0 {
				segments = append(segments, pattern[start:i])
				start = i + 1
			}
		case '|':
			if classes == 0 && groups == 0 {
				if err := finish(i); err != nil {
					return nil, err
				}
				start = i + 1
			}
		}
	}
	if err := finish(len(pattern)); err != nil {
		return nil, err
	}
	return branches, nil
}

func nativeTopMatch(branches []nativePatternBranch, name string) (bool, bool) {
	for _, branch := range branches {
		if branch.first.MatchString(name) {
			return true, branch.partial
		}
	}
	return false, false
}
