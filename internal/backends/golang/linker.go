package golang

import (
	"context"
	"fmt"
	"go/token"
	"path/filepath"
	"slices"
	"strings"

	"github.com/greatliontech/gofresh/gotool"
	"github.com/greatliontech/gofresh/runtimeinput"
	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"golang.org/x/mod/module"
)

// selectedInputFile preserves the frozen association checked at each attempt.
type selectedInputFile struct {
	Name string
	Path string
}

func inputFileNames(files []selectedInputFile) []string {
	var names []string
	for _, f := range files {
		names = append(names, f.Name)
	}
	return names
}

func inputFilePaths(files []selectedInputFile) []string {
	var paths []string
	for _, f := range files {
		paths = append(paths, f.Path)
	}
	return paths
}

// portableEnvName admits names with the same spelling on supported hosts.
func portableEnvName(s string) bool {
	if s == "" {
		return false
	}
	for i, c := range s {
		if c != '_' && !(c >= 'A' && c <= 'Z') && !(c >= 'a' && c <= 'z') && !(i > 0 && c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

// linkerValue admits the single-token -X subset without a quoting language.
// Embedded equals signs survive: the linker splits only the first one.
func linkerValue(s string) error {
	for _, c := range s {
		if c <= ' ' || c > '~' || c == '\'' || c == '"' || c == '\\' {
			return fmt.Errorf("linker string value contains unsupported whitespace, quoting or non-ASCII bytes")
		}
	}
	return nil
}

func validateLinkerSelection(cfg *stipulatorv1.GoInvocationConfig) error {
	previous := ""
	for _, x := range cfg.GetLinkerStrings() {
		if x == nil || module.CheckImportPath(x.GetPackage()) != nil || !token.IsIdentifier(x.GetVariable()) {
			return fmt.Errorf("linker_strings needs a package import path and variable identifier")
		}
		key := x.GetPackage() + "\x00" + x.GetVariable()
		if key <= previous {
			return fmt.Errorf("linker_strings targets must be unique and sorted by package and variable")
		}
		previous = key
		switch {
		case x.HasValue():
			if err := linkerValue(x.GetValue()); err != nil {
				return err
			}
		case x.HasValueEnvironment():
			if !portableEnvName(x.GetValueEnvironment()) {
				return fmt.Errorf("linker_strings value_environment is not a portable environment name")
			}
		default:
			return fmt.Errorf("linker_strings needs exactly one value source")
		}
	}
	previous = ""
	for _, name := range cfg.GetInputFilesFromEnvironment() {
		if !portableEnvName(name) || name <= previous {
			return fmt.Errorf("input_files_from_environment needs unique sorted portable names")
		}
		previous = name
	}
	return nil
}

// splitGoflagsWords splits a GOFLAGS string into words while preserving
// single- or double-quoted spans (such as -ldflags="-X a.b=1 -X c.d=2").
func splitGoflagsWords(s string) ([]string, error) {
	var words []string
	var cur strings.Builder
	inWord := false
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			cur.WriteByte(c)
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '"' || c == '\'' {
			inWord = true
			quote = c
			cur.WriteByte(c)
			continue
		}
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			if inWord {
				words = append(words, cur.String())
				cur.Reset()
				inWord = false
			}
			continue
		}
		inWord = true
		cur.WriteByte(c)
	}
	if quote != 0 {
		return nil, fmt.Errorf("GOFLAGS has unclosed quote")
	}
	if inWord {
		words = append(words, cur.String())
	}
	return words, nil
}

func stripOuterQuotes(s string) string {
	if len(s) >= 2 && ((s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'')) {
		return s[1 : len(s)-1]
	}
	return s
}

func parseLdflagsAssignments(raw string) ([]*stipulatorv1.GoLinkerString, error) {
	fields := strings.Fields(stripOuterQuotes(raw))
	if len(fields) == 0 {
		return nil, fmt.Errorf("GOFLAGS carries -ldflags, which is unsupported: empty -ldflags")
	}
	var out []*stipulatorv1.GoLinkerString
	for i := 0; i < len(fields); {
		tok := fields[i]
		var assign string
		switch {
		case tok == "-X" || tok == "--X":
			if i+1 >= len(fields) {
				return nil, fmt.Errorf("GOFLAGS carries -ldflags, which is unsupported: -X requires package.variable=value")
			}
			assign = fields[i+1]
			i += 2
		case strings.HasPrefix(tok, "-X=") || strings.HasPrefix(tok, "--X="):
			assign = tok[strings.IndexByte(tok, '=')+1:]
			i++
		default:
			return nil, fmt.Errorf("GOFLAGS carries -ldflags, which is unsupported: non-X linker flag %q", tok)
		}
		target, value, ok := strings.Cut(assign, "=")
		if !ok {
			return nil, fmt.Errorf("GOFLAGS carries -ldflags, which is unsupported: -X assignment %q must be package.variable=value", assign)
		}
		dot := strings.LastIndexByte(target, '.')
		if dot <= 0 || dot == len(target)-1 {
			return nil, fmt.Errorf("GOFLAGS carries -ldflags, which is unsupported: -X target %q must be package.variable", target)
		}
		pkg, variable := target[:dot], target[dot+1:]
		if module.CheckImportPath(pkg) != nil || !token.IsIdentifier(variable) {
			return nil, fmt.Errorf("GOFLAGS carries -ldflags, which is unsupported: linker_strings needs a package import path and variable identifier")
		}
		if err := linkerValue(value); err != nil {
			return nil, fmt.Errorf("GOFLAGS carries -ldflags, which is unsupported: %w", err)
		}
		ls := &stipulatorv1.GoLinkerString{}
		ls.SetPackage(pkg)
		ls.SetVariable(variable)
		ls.SetValue(value)
		out = append(out, ls)
	}
	return out, nil
}

// extractGoflagsLinkerStrings validates a GOFLAGS value, auto-extracting any
// -ldflags="-X pkg.var=val ..." entries into canonical GoLinkerString entries
// merged with explicit and stripping -ldflags from the retained GOFLAGS string.
func extractGoflagsLinkerStrings(goflags string, explicit []*stipulatorv1.GoLinkerString) ([]*stipulatorv1.GoLinkerString, string, error) {
	words, err := splitGoflagsWords(goflags)
	if err != nil {
		return nil, "", err
	}
	var kept []string
	var extracted []*stipulatorv1.GoLinkerString
	for i := 0; i < len(words); i++ {
		rawWord := words[i]
		w := stripOuterQuotes(rawWord)
		trimmed := strings.TrimLeft(w, "-")
		name, val, hasEq := strings.Cut(trimmed, "=")
		if name == "ldflags" {
			if !hasEq {
				if i+1 >= len(words) {
					return nil, "", fmt.Errorf("GOFLAGS carries -ldflags, which is unsupported: empty -ldflags")
				}
				i++
				val = words[i]
			}
			val = stripOuterQuotes(val)
			if (val == "-X" || val == "--X") && i+1 < len(words) && !strings.HasPrefix(words[i+1], "-") {
				i++
				val = val + " " + stripOuterQuotes(words[i])
			}
			for i+1 < len(words) {
				next := stripOuterQuotes(words[i+1])
				if (next == "-X" || next == "--X") && i+2 < len(words) {
					val = val + " " + next + " " + stripOuterQuotes(words[i+2])
					i += 2
					continue
				}
				if strings.HasPrefix(next, "-X=") || strings.HasPrefix(next, "--X=") {
					val = val + " " + next
					i++
					continue
				}
				break
			}
			parsed, err := parseLdflagsAssignments(val)
			if err != nil {
				return nil, "", err
			}
			extracted = append(extracted, parsed...)
			continue
		}
		if reason, ok := unsupportedGoflags[name]; ok {
			return nil, "", fmt.Errorf("GOFLAGS carries -%s, which is unsupported: %s", name, reason)
		}
		if selectionGoflags[name] {
			return nil, "", fmt.Errorf("GOFLAGS carries -%s, which shapes test selection outside the reviewed record", name)
		}
		if owner, ok := ownedGoflags[name]; ok {
			return nil, "", fmt.Errorf("GOFLAGS carries -%s, which is owned by %s; declare it there", name, owner)
		}
		kept = append(kept, rawWord)
	}
	if len(extracted) == 0 {
		return explicit, strings.Join(kept, " "), nil
	}
	combined := make([]*stipulatorv1.GoLinkerString, 0, len(explicit)+len(extracted))
	seen := map[string]bool{}
	for _, x := range explicit {
		key := x.GetPackage() + "\x00" + x.GetVariable()
		seen[key] = true
		combined = append(combined, x)
	}
	for _, x := range extracted {
		key := x.GetPackage() + "\x00" + x.GetVariable()
		if seen[key] {
			return nil, "", fmt.Errorf("linker_strings targets must be unique and sorted by package and variable")
		}
		seen[key] = true
		combined = append(combined, x)
	}
	slices.SortFunc(combined, func(a, b *stipulatorv1.GoLinkerString) int {
		ka := a.GetPackage() + "\x00" + a.GetVariable()
		kb := b.GetPackage() + "\x00" + b.GetVariable()
		return strings.Compare(ka, kb)
	})
	return combined, strings.Join(kept, " "), nil
}

// resolveLinkerSelection freezes explicit policy selections and any -ldflags
// -X assignments extracted from GOFLAGS after environment admission.
func resolveLinkerSelection(n *NormalizedInvocation, cfg *stipulatorv1.GoInvocationConfig) error {
	linkerStrings := cfg.GetLinkerStrings()
	if len(n.extractedLinkerStrings) > 0 {
		linkerStrings = n.extractedLinkerStrings
	} else if cfg.HasGoflags() {
		extracted, _, err := extractGoflagsLinkerStrings(cfg.GetGoflags(), linkerStrings)
		if err != nil {
			return err
		}
		linkerStrings = extracted
	}
	for _, x := range linkerStrings {
		value := x.GetValue()
		kind, source := "literal", value
		if x.HasValueEnvironment() {
			kind, source = "environment", x.GetValueEnvironment()
			var ok bool
			value, ok = gotool.LookupEnv(n.Env, source)
			if !ok || value == "" {
				return fmt.Errorf("linker_strings environment %s is unset or empty", source)
			}
		}
		if err := linkerValue(value); err != nil {
			return fmt.Errorf("linker_strings %s.%s: %w", x.GetPackage(), x.GetVariable(), err)
		}
		n.LinkerAssignments = append(n.LinkerAssignments, x.GetPackage()+"."+x.GetVariable()+"="+value)
		n.DeclaredLinkerStrings = append(n.DeclaredLinkerStrings, string(quotedList([]string{x.GetPackage(), x.GetVariable(), kind, source})))
	}
	seen := map[string]bool{}
	if len(cfg.GetInputFilesFromEnvironment()) != 0 {
		n.inputRoots = &runtimeinput.Roots{}
	}
	for _, name := range cfg.GetInputFilesFromEnvironment() {
		p, ok := gotool.LookupEnv(n.Env, name)
		if !ok || p == "" || !filepath.IsAbs(p) || filepath.Clean(p) != p {
			return fmt.Errorf("input file environment %s must name a clean absolute file path", name)
		}
		if seen[p] {
			return fmt.Errorf("input file environment %s repeats selected file %s", name, p)
		}
		seen[p] = true
		n.InputFiles = append(n.InputFiles, selectedInputFile{Name: name, Path: p})
	}
	return nil
}

// requireExactInputEligibility gates the actual or prospective retained runtime,
// independently of native preparation. Eligibility grants no read or outcome
// evidence; only the observation owner decides whether bytes are observable.
func requireExactInputEligibility(ctx context.Context, n *NormalizedInvocation, runtime *retainedEnvironment) error {
	for _, f := range n.InputFiles {
		value, ok := gotool.LookupEnv(runtime.Env, f.Name)
		if !ok || value != f.Path {
			return fmt.Errorf("input file environment %s no longer selects frozen file %s", f.Name, f.Path)
		}
		result, err := runtimeinput.FileInputEligibility(ctx, treeRoot(n), runtime.Dir, f.Path, runtimeinput.FileInputContext{
			Env: runtime.Env, Runner: ownedRunner, Roots: n.inputRoots, ExcludedPaths: n.ExcludedPaths,
		})
		if err != nil {
			return fmt.Errorf("input file environment %s (%s): %w", f.Name, f.Path, err)
		}
		if result.Kind != runtimeinput.RawFileInput {
			return fmt.Errorf("input file environment %s (%s) is not raw-byte eligible: %s", f.Name, f.Path, result.Reason)
		}
	}
	return nil
}

// linkerBuildFlags is shared by execution and analysis, so the build guard
// describes the same resolved assignments as every retained compilation.
func linkerBuildFlags(assignments []string) []string {
	if len(assignments) == 0 {
		return nil
	}
	var parts []string
	for _, x := range assignments {
		parts = append(parts, "-X", x)
	}
	return []string{"-ldflags=" + strings.Join(parts, " ")}
}
