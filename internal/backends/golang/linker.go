package golang

import (
	"context"
	"fmt"
	"go/token"
	"path/filepath"
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

// resolveLinkerSelection freezes explicit policy selections after environment
// admission. It neither reads GOFLAGS as a linker source nor runs a producer.
func resolveLinkerSelection(n *NormalizedInvocation, cfg *stipulatorv1.GoInvocationConfig) error {
	for _, x := range cfg.GetLinkerStrings() {
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
