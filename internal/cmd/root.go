// Package cmd wires the CLI: thin cobra commands over the compile, verify,
// coverage, author, and records packages. No verb logic lives here.
package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/author"
	"github.com/greatliontech/stipulator/internal/backends/golang"
	"github.com/greatliontech/stipulator/internal/compile"
	"github.com/greatliontech/stipulator/internal/corpus"
	"github.com/greatliontech/stipulator/internal/verify"
)

// chdir is the repository root, shared by every verb.
var chdir string

// Execute runs the CLI.
func Execute() error {
	return newRootCmd().Execute()
}

func newRootCmd() *cobra.Command {
	c := &cobra.Command{
		Use:           "stipulator",
		Short:         "Specification compiler and conformance verifier",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	c.PersistentFlags().StringVarP(&chdir, "chdir", "C", ".", "start directory for corpus-root discovery")
	c.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		// Root-level init scaffolds a new root exactly where it is invoked
		// — nested corpora are deliberate — and help-class commands need
		// no corpus. Subcommands merely named "init" (`policy init`)
		// operate on an existing corpus and fall through to discovery.
		switch cmd.Name() {
		case "init":
			if cmd.Parent() == cmd.Root() {
				return nil
			}
		case "help", "completion", cobra.ShellCompRequestCmd, cobra.ShellCompNoDescRequestCmd:
			return nil
		case golang.ResolverSubcommand:
			// The resolver child's argument is a tree root its parent
			// already resolved; corpus-root discovery does not apply.
			return nil
		case "mcp":
			// A globally-registered server must start even outside a
			// corpus: tools return the teaching error per request.
			if root, err := corpus.FindRoot(chdir); err == nil {
				chdir = root
			}
			return nil
		}
		root, err := corpus.FindRoot(chdir)
		if err != nil {
			return err
		}
		chdir = root
		return nil
	}
	c.AddCommand(compileCmd(), checkCmd(), verifyCmd(), gateCmd(), bindCmd(), unbindCmd(), gapCmd(), diffCmd(), impactCmd(), pruneCmd(), pinCmd(), disposeCmd(), targetsCmd(), attestCmd(), initCmd(), policyCmd(), mcpCmd(), internalResolveCmd())
	return c
}

// mustCompile compiles the corpus at dir, printing diagnostics and exiting
// non-zero on profile violations.
func mustCompile(dir string) (*stipulatorv1.Spec, error) {
	spec, diags, err := compile.Compile(os.DirFS(dir))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) && strings.Contains(err.Error(), corpus.ManifestPath) {
			return nil, fmt.Errorf("not a stipulator repository (no %s); run `stipulator init` to scaffold one", corpus.ManifestPath)
		}
		return nil, err
	}
	return mustClean(spec, diags)
}

// mustCompileFS compiles a corpus from any filesystem — a git revision's
// tree, most notably — labeling errors with where it came from.
func mustCompileFS(fsys fs.FS, label string) (*stipulatorv1.Spec, error) {
	spec, diags, err := compile.Compile(fsys)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) && strings.Contains(err.Error(), corpus.ManifestPath) {
			return nil, fmt.Errorf("%s holds no stipulator corpus (no %s)", label, corpus.ManifestPath)
		}
		return nil, err
	}
	return mustClean(spec, diags)
}

func mustClean(spec *stipulatorv1.Spec, diags []compile.Diagnostic) (*stipulatorv1.Spec, error) {
	for _, d := range diags {
		fmt.Fprintln(os.Stderr, d)
	}
	if len(compile.Errors(diags)) > 0 {
		os.Exit(1)
	}
	return spec, nil
}

func makeBackends(ctx context.Context, dir string) (map[string]verify.Backend, error) {
	gb, err := golang.NewOwned(ctx, dir)
	if err != nil {
		return nil, err
	}
	return map[string]verify.Backend{"go": gb}, nil
}

func writeFileAt(dir, rel string, content []byte) error {
	full := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	fmt.Println("wrote", rel)
	return os.WriteFile(full, content, 0o644)
}

// applyUpdates applies a batch under compare-and-swap: every
// precondition is checked before the first write — a target that moved
// since the operation read it refuses the WHOLE batch, so a concurrent
// agent's records are never silently dropped — then every write stages
// to a temp file before the first rename, shrinking a mid-batch fault
// to at most a git-visible partial state (REQ-record-cas).
func applyUpdates(dir string, ups []author.Update) error {
	seen := map[string]bool{}
	for _, up := range ups {
		// A duplicate path would pass every pre-batch precondition and
		// then last-write-wins silently; no verb produces one today, so
		// reaching this is a programming error, refused loudly.
		if seen[up.Path] {
			return fmt.Errorf("batch names %s twice; refusing the ambiguous apply", up.Path)
		}
		seen[up.Path] = true
		if err := checkPrior(dir, up); err != nil {
			return err
		}
	}
	type staged struct {
		tmp, full, path string
	}
	var writes []staged
	var deletions []author.Update
	// Any temp not renamed by the time we return is removed: a leaked
	// dot-temp is invisible to the record loader, but tidiness is free.
	defer func() {
		for _, w := range writes {
			os.Remove(w.tmp)
		}
	}()
	for _, up := range ups {
		full := filepath.Join(dir, filepath.FromSlash(up.Path))
		if up.Content == nil {
			deletions = append(deletions, up)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		tmp, err := os.CreateTemp(filepath.Dir(full), ".stipulator-apply-*")
		if err != nil {
			return err
		}
		// Registered before the write so the deferred cleanup owns it on
		// every failure path.
		writes = append(writes, staged{tmp: tmp.Name(), full: full, path: up.Path})
		if _, err := tmp.Write(up.Content); err != nil {
			tmp.Close()
			return err
		}
		if err := tmp.Close(); err != nil {
			return err
		}
	}
	renamed := 0
	for i := range writes {
		if err := os.Rename(writes[i].tmp, writes[i].full); err != nil {
			return err
		}
		renamed++
		fmt.Println("wrote", writes[i].path)
	}
	writes = writes[renamed:]
	for _, up := range deletions {
		if err := os.Remove(filepath.Join(dir, filepath.FromSlash(up.Path))); err != nil {
			return err
		}
		fmt.Println("deleted", up.Path)
	}
	return nil
}

// checkPrior is one update's compare-and-swap precondition against the
// tree.
func checkPrior(dir string, up author.Update) error {
	// An update carrying neither a prior nor read-absence was never
	// stamped: a stamped update always sets one (fs.ReadFile returns
	// non-nil even for an empty file). Refusing makes a missing stamp
	// loud at apply time instead of a silent CAS hole.
	if up.Prior == nil && !up.PriorAbsent {
		return fmt.Errorf("%s carries no precondition; the computing operation failed to stamp what it read", up.Path)
	}
	full := filepath.Join(dir, filepath.FromSlash(up.Path))
	current, err := os.ReadFile(full)
	switch {
	case os.IsNotExist(err):
		if !up.PriorAbsent && up.Prior != nil {
			return fmt.Errorf("%s vanished since the operation read it; re-run against the current tree", up.Path)
		}
		return nil
	case err != nil:
		return err
	case up.PriorAbsent:
		return fmt.Errorf("%s appeared since the operation ran; re-run against the current tree", up.Path)
	case !bytes.Equal(current, up.Prior):
		return fmt.Errorf("%s changed since the operation read it (a concurrent write?); re-run against the current tree", up.Path)
	}
	return nil
}
