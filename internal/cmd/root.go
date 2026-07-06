// Package cmd wires the CLI: thin cobra commands over the compile, verify,
// coverage, author, and records packages. No verb logic lives here.
package cmd

import (
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
	c := &cobra.Command{
		Use:           "stipulator",
		Short:         "Specification compiler and conformance verifier",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	c.PersistentFlags().StringVarP(&chdir, "chdir", "C", ".", "repository root")
	c.AddCommand(compileCmd(), verifyCmd(), gateCmd(), bindCmd(), unbindCmd(), gapCmd(), diffCmd(), pinCmd(), disposeCmd(), hardenCmd(), initCmd(), mcpCmd())
	return c.Execute()
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
	if len(diags) > 0 {
		for _, d := range diags {
			fmt.Fprintln(os.Stderr, d)
		}
		os.Exit(1)
	}
	return spec, nil
}

func makeBackends(dir string) (map[string]verify.Backend, error) {
	gb, err := golang.New(dir)
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

func applyUpdates(dir string, ups []author.Update) error {
	for _, up := range ups {
		full := filepath.Join(dir, filepath.FromSlash(up.Path))
		if up.Content == nil {
			if err := os.Remove(full); err != nil {
				return err
			}
			fmt.Println("deleted", up.Path)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(full, up.Content, 0o644); err != nil {
			return err
		}
		fmt.Println("wrote", up.Path)
	}
	return nil
}
