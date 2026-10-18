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

// portableEnvName is deliberately narrower than a platform's environment:
// declarations must have the same name and meaning on every supported host.
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

// linkerValue admits the single-token -X subset without a second quoting
// language. '=' within a value is preserved: the linker splits only the first.
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

// resolveLinkerSelection freezes only explicit policy selections. It neither
// reads GOFLAGS as a second linker source nor runs a value producer.
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
	for _, name := range cfg.GetInputFilesFromEnvironment() {
		p, ok := gotool.LookupEnv(n.Env, name)
		if !ok || p == "" || !filepath.IsAbs(p) || filepath.Clean(p) != p {
			return fmt.Errorf("input file environment %s must name a clean absolute file path", name)
		}
		if seen[p] {
			return fmt.Errorf("input file environment %s repeats selected file %s", name, p)
		}
		seen[p] = true
		n.InputFiles = append(n.InputFiles, p)
		n.BracketPaths = append(n.BracketPaths, p)
	}
	n.InputFileEnvironment = append([]string(nil), cfg.GetInputFilesFromEnvironment()...)
	return nil
}

// requireExactInputEligibility asks the observation owner, under the environment
// and package directory its actual ingest uses. A bracket cannot turn a
// recordless cache read or a machine projection into a raw-byte dependency.
func requireExactInputEligibility(ctx context.Context, n *NormalizedInvocation, pkgDir string) error {
	if len(n.InputFiles) == 0 {
		return nil
	}
	if len(n.InputFiles) != len(n.InputFileEnvironment) {
		return fmt.Errorf("exact input files lost their environment-name associations")
	}
	memo := n.roots
	if memo == nil {
		memo = &runtimeinput.Roots{}
	}
	env := gotool.SetEnv(witnessEnvOf(n), "PWD", pkgDir)
	for i, p := range n.InputFiles {
		result, err := runtimeinput.FileInputEligibility(ctx, treeRoot(n), pkgDir, p, runtimeinput.FileInputContext{
			Env: env, Runner: rootsRunner, Roots: memo, ExcludedPaths: n.ExcludedPaths,
		})
		if err != nil {
			return fmt.Errorf("input file environment %s (%s): %w", n.InputFileEnvironment[i], p, err)
		}
		if result.Kind != runtimeinput.RawFileInput {
			return fmt.Errorf("input file environment %s (%s) is not raw-byte eligible: %s", n.InputFileEnvironment[i], p, result.Reason)
		}
	}
	return nil
}

// linkerBuildFlags is shared by execution and freshness: every resolved
// assignment changes the producing binary and its build-config guard together.
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
