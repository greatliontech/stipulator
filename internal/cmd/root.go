// Package cmd wires the CLI: thin cobra commands over the compile, verify,
// coverage, author, and records packages. No verb logic lives here.
package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/author"
	"github.com/greatliontech/stipulator/internal/backends/golang"
	"github.com/greatliontech/stipulator/internal/check"
	"github.com/greatliontech/stipulator/internal/compile"
	"github.com/greatliontech/stipulator/internal/corpus"
	"github.com/greatliontech/stipulator/internal/progress"
	"github.com/greatliontech/stipulator/internal/remedy"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/internal/views"
)

// chdir is the repository root, shared by every verb.
var chdir string

// ExitStatus is a verb's failing verdict carried as an exit code: a
// failing check, verify, gate, or compile returns it instead of exiting
// in place, so every run — failing ones above all — passes through
// execute's ending, which seals the verdict as a test failure.
type ExitStatus struct{ Code int }

func (e ExitStatus) Error() string { return fmt.Sprintf("exit status %d", e.Code) }

func exitStatus(code int) error { return ExitStatus{Code: code} }

// Interrupted is a run ended by its context — a cancellation or a
// deadline — before a verdict: the verdict a verb produced under the
// dying context is discarded, and the process's disposition is the
// interruption's own, decided where the interruption is known (main,
// from the signal it received), never a verdict's exit code.
type Interrupted struct{ Cause error }

func (e Interrupted) Error() string {
	if e.Cause == nil {
		return "interrupted"
	}
	return "interrupted: " + e.Cause.Error()
}

func (e Interrupted) Unwrap() error { return e.Cause }

// Execute runs the CLI under ctx. One progress reporter serves the
// whole invocation, rendering the same events the MCP surface carries
// as notifications as dim stderr lines (REQ-mcp-progress's both-surface
// leg); at the end a run that reached a verdict prints its phase
// timings — the pace line — and an interrupted one names the phase it
// died in and what it kept. The terminal cause follows the MCP
// surface's vocabulary: a verdict that fails is a test failure, an
// operational fault a failure, an interruption its own cause.
func Execute(ctx context.Context) error {
	status := dimWriter{os.Stderr}
	return execute(ctx, os.Args[1:], progress.Stderr(status), status)
}

// execute is Execute over explicit arguments, progress sink, and the
// status stream the pace line goes to: the seam the in-process tests
// drive. An interrupted run returns Interrupted whatever its verb
// returned — no partial verdict passes for a pass or a fail.
func execute(ctx context.Context, args []string, sink func(*stipulatorv1.ProgressEvent), status io.Writer) error {
	prog := progress.New(sink)
	root := newRootCmd()
	root.SetArgs(args)
	err := root.ExecuteContext(progress.NewContext(ctx, prog))
	var verdict ExitStatus
	switch {
	case errors.Is(ctx.Err(), context.Canceled):
		prog.Seal(stipulatorv1.TerminalCause_TERMINAL_CAUSE_CANCELLED)
		return Interrupted{Cause: ctx.Err()}
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		prog.Seal(stipulatorv1.TerminalCause_TERMINAL_CAUSE_DEADLINE)
		return Interrupted{Cause: ctx.Err()}
	default:
		if stamps := prog.Stamps(); stamps != "" {
			fmt.Fprintln(status, stamps)
		}
		switch {
		case err == nil:
			prog.Seal(stipulatorv1.TerminalCause_TERMINAL_CAUSE_COMPLETED)
		case errors.As(err, &verdict):
			prog.Seal(stipulatorv1.TerminalCause_TERMINAL_CAUSE_TEST_FAILURE)
		default:
			prog.Seal(stipulatorv1.TerminalCause_TERMINAL_CAUSE_SERVER_FAILURE)
		}
	}
	return err
}

// dimWriter renders each line the progress sink writes dim, the
// stderr status style every verb uses.
type dimWriter struct{ w io.Writer }

func (d dimWriter) Write(p []byte) (int, error) {
	if _, err := io.WriteString(d.w, dim(strings.TrimRight(string(p), "\n"))+"\n"); err != nil {
		return 0, err
	}
	return len(p), nil
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
		case "guidance":
			// Embedded content: guidance answers before a corpus
			// exists, exactly like the document it serves.
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
	c.AddCommand(compileCmd(), checkCmd(), verifyCmd(), gateCmd(), explainCmd(), bindCmd(), unbindCmd(), gapCmd(), diffCmd(), impactCmd(), pruneCmd(), pinCmd(), disposeCmd(), retargetCmd(), attestCmd(), initCmd(), policyCmd(), mcpCmd(), guidanceCmd(), internalResolveCmd())
	renderKnobUsage(c)
	return c
}

// mustPrepare gathers an operation's held inputs — corpus, records,
// coverage policy, record hygiene — before its first child process
// (REQ-check-preparation), rendering compile diagnostics as mustCompile
// does: errors end the command.
func mustPrepare(dir string) (*check.Prepared, error) {
	prepared, err := check.Prepare(os.DirFS(dir))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) && strings.Contains(err.Error(), corpus.ManifestPath) {
			return nil, fmt.Errorf("not a stipulator repository (no %s); run `%s` to scaffold one", corpus.ManifestPath, remedy.Init())
		}
		return nil, err
	}
	if _, err := mustClean(prepared.Spec, prepared.Diagnostics); err != nil {
		return nil, err
	}
	return prepared, nil
}

// refuseHygiene prints the record-only verification problems and fails
// the command before any witness executes: verification cannot pass
// whatever a run would say (REQ-check-preparation).
func refuseHygiene(problems []verify.Problem) error {
	if len(problems) == 0 {
		return nil
	}
	for _, p := range problems {
		fmt.Fprintln(os.Stderr, red(p.String()))
	}
	return fmt.Errorf("fix verification problems first (%d)", len(problems))
}

// mustCompile compiles the corpus at dir, printing diagnostics and exiting
// non-zero on profile violations.
func mustCompile(dir string) (*stipulatorv1.Spec, error) {
	spec, diags, err := compile.Compile(os.DirFS(dir))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) && strings.Contains(err.Error(), corpus.ManifestPath) {
			return nil, fmt.Errorf("not a stipulator repository (no %s); run `%s` to scaffold one", corpus.ManifestPath, remedy.Init())
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
		return nil, exitStatus(1)
	}
	return spec, nil
}

// makeBackends builds the declaration-reading backends and the closer
// that ends their children — the backend's own Close, so a verb that
// defers it ends the resolver child with the verb, not with the
// process. The whole-tree form publishes nothing, so the close's error
// is the child's end and no verb's result depends on it. A variable so
// a test can stand in a recording backend and pin that every verb
// closes what it built.
var makeBackends = func(ctx context.Context, dir string) (map[string]verify.Backend, func() error, error) {
	gb, err := golang.NewWholeTree(ctx, dir)
	if err != nil {
		return nil, nil, err
	}
	return map[string]verify.Backend{"go": gb}, gb.Close, nil
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

// prepareScoped is the prologue every scoped query verb shares: the
// corpus and records prepared, then every refusal the held inputs
// decide — the scope vocabulary, the view word, the requirement
// identifiers, and the records' hygiene — fired before any witness
// executes (REQ-check-preparation). One prologue, so a verb cannot
// validate its vocabulary after the run it was meant to spare.
func prepareScoped(scope views.Scope, validateView func(string) error, view string) (*check.Prepared, views.Scope, error) {
	prepared, err := mustPrepare(chdir)
	if err != nil {
		return nil, scope, err
	}
	if err := scope.Validate(); err != nil {
		return nil, scope, err
	}
	if err := validateView(view); err != nil {
		return nil, scope, err
	}
	if err := check.KnownIDs(prepared.Spec, scope.Ids); err != nil {
		return nil, scope, err
	}
	if err := refuseHygiene(prepared.Hygiene); err != nil {
		return nil, scope, err
	}
	return prepared, scope, nil
}
