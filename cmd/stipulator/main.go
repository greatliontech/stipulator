// Command stipulator compiles and verifies a specification corpus.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/author"
	"github.com/greatliontech/stipulator/internal/backends/golang"
	"github.com/greatliontech/stipulator/internal/compile"
	"github.com/greatliontech/stipulator/internal/coverage"
	"github.com/greatliontech/stipulator/internal/diff"
	"github.com/greatliontech/stipulator/internal/records"
	"github.com/greatliontech/stipulator/internal/verify"
	"google.golang.org/protobuf/encoding/prototext"
)

// knownBackends closes the backend-name set so a typo cannot author an
// unvalidated binding.
var knownBackends = map[string]bool{"go": true, "proto": true}

var root string

func main() {
	cmd := &cobra.Command{
		Use:           "stipulator",
		Short:         "Specification compiler and conformance verifier",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.PersistentFlags().StringVarP(&root, "chdir", "C", ".", "repository root")
	cmd.AddCommand(compileCmd(), verifyCmd(), gateCmd(), bindCmd(), unbindCmd(), gapCmd(), diffCmd(), pinCmd())
	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "stipulator:", err)
		os.Exit(2)
	}
}

func compileCmd() *cobra.Command {
	var ir bool
	c := &cobra.Command{
		Use:   "compile",
		Short: "Compile the corpus; print diagnostics",
		RunE: func(cmd *cobra.Command, args []string) error {
			spec, err := mustCompile(root)
			if err != nil {
				return err
			}
			if ir {
				b, err := prototext.MarshalOptions{Multiline: true}.Marshal(spec)
				if err != nil {
					return err
				}
				os.Stdout.Write(b)
				return nil
			}
			fmt.Printf("ok: %d documents, %d requirements, %d terms, %d notes, %d annotations, %d edges\n",
				len(spec.GetDocuments()), len(spec.GetRequirements()), len(spec.GetTerms()),
				len(spec.GetNotes()), len(spec.GetAnnotations()), len(spec.GetEdges()))
			return nil
		},
	}
	c.Flags().BoolVar(&ir, "ir", false, "print the compiled IR as textproto")
	return c
}

func verifyCmd() *cobra.Command {
	var noTest bool
	c := &cobra.Command{
		Use:   "verify",
		Short: "Check records against the corpus and code",
		RunE: func(cmd *cobra.Command, args []string) error {
			spec, err := mustCompile(root)
			if err != nil {
				return err
			}
			store, err := records.Load(os.DirFS(root))
			if err != nil {
				return err
			}
			var testRun *verify.TestRun
			if !noTest {
				tr, err := golang.RunTests(root)
				if err != nil {
					return err
				}
				testRun = tr
			}
			backends, err := makeBackends(root)
			if err != nil {
				return err
			}
			rep := verify.Run(spec, store, backends, testRun)
			for _, p := range rep.Problems {
				fmt.Fprintln(os.Stderr, p)
			}
			for _, r := range rep.Results {
				if r.Resolution == verify.NotFound {
					fmt.Fprintf(os.Stderr, "%s: broken: symbol %s not found (binding for %s)\n", r.Path, r.Symbol, r.RequirementId)
				}
				if r.Shape == verify.ShapeMismatch {
					fmt.Fprintf(os.Stderr, "%s: broken: shape of %s moved (binding for %s)\n", r.Path, r.Symbol, r.RequirementId)
				}
				if r.TestOutcome == verify.TestFailed {
					fmt.Fprintf(os.Stderr, "%s: broken: bound test %s failed (binding for %s)\n", r.Path, r.Symbol, r.RequirementId)
				}
				if testRun != nil && r.TestOutcome == verify.TestNotRun && r.Role == stipulatorv1.BindingRole_BINDING_ROLE_TESTS {
					fmt.Fprintf(os.Stderr, "%s: broken: bound test %s produced no outcome — unwitnessed (binding for %s)\n", r.Path, r.Symbol, r.RequirementId)
				}
			}
			fmt.Printf("bindings: %d pinned, %d stale; shapes: %d pinned, %d unpinned; broken: %d symbols, %d shapes, %d failed tests, %d unwitnessed; unverified: %d; tests passed: %d; registrations: %d; gaps: %d\n",
				rep.Pinned, rep.Stale, rep.ShapePinned, rep.ShapeUnpinned,
				rep.Broken, rep.ShapeMismatch, rep.TestsFailed, rep.TestsNotRun,
				rep.Unverified, rep.TestsPassed, len(rep.Registrations), len(store.Gaps))
			// verify fails only on verification errors; red evidence is
			// bucket data for the gate, which decides gap-excusability.
			if len(rep.Problems) > 0 {
				os.Exit(1)
			}
			return nil
		},
	}
	c.Flags().BoolVar(&noTest, "no-test", false, "skip running tests (no witnesses)")
	return c
}

func gateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "gate",
		Short: "Coverage buckets and the gate verdict",
		RunE: func(cmd *cobra.Command, args []string) error {
			spec, err := mustCompile(root)
			if err != nil {
				return err
			}
			store, err := records.Load(os.DirFS(root))
			if err != nil {
				return err
			}
			testRun, err := golang.RunTests(root)
			if err != nil {
				return err
			}
			backends, err := makeBackends(root)
			if err != nil {
				return err
			}
			rep := verify.Run(spec, store, backends, testRun)
			for _, p := range rep.Problems {
				fmt.Fprintln(os.Stderr, p)
			}
			if len(rep.Problems) > 0 {
				os.Exit(1)
			}
			cov := coverage.Evaluate(spec, rep, store, true)
			counts := map[coverage.Bucket]int{}
			for _, r := range cov.Requirements {
				counts[r.Bucket]++
				if r.Bucket != coverage.Covered && r.Bucket != coverage.Exempt {
					fmt.Printf("%s: %s (%s)\n", r.Bucket, r.Id, strings.Join(r.Reasons, "; "))
				}
			}
			for _, g := range cov.Gaps {
				fmt.Printf("gap %s: %s (%s)\n", g.State, g.RequirementId, g.Path)
			}
			fmt.Printf("coverage: %d covered, %d uncovered, %d stale, %d broken, %d exempt; gaps: %d\n",
				counts[coverage.Covered], counts[coverage.Uncovered], counts[coverage.Stale],
				counts[coverage.Broken], counts[coverage.Exempt], len(cov.Gaps))
			if !cov.GatePasses() {
				for _, v := range cov.Violations {
					fmt.Fprintf(os.Stderr, "gate: %s is red and no gap names it\n", v)
				}
				os.Exit(1)
			}
			fmt.Println("gate: pass")
			return nil
		},
	}
}

func bindCmd() *cobra.Command {
	var req, symbol, role, backendName, file string
	c := &cobra.Command{
		Use:   "bind",
		Short: "Author a validated binding claim",
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := author.ParseRole(role)
			if err != nil {
				return err
			}
			if !knownBackends[backendName] {
				return fmt.Errorf("unknown backend %q (go, proto)", backendName)
			}
			backends, err := makeBackends(root)
			if err != nil {
				return err
			}
			up, err := author.Bind(os.DirFS(root), backends, author.BindRequest{
				Requirement: req, Symbol: symbol, Backend: backendName,
				Role: r, File: file,
			})
			if err != nil {
				return err
			}
			return applyUpdates(root, []author.Update{*up})
		},
	}
	c.Flags().StringVar(&req, "req", "", "requirement identifier")
	c.Flags().StringVar(&symbol, "symbol", "", "backend-scoped symbol reference")
	c.Flags().StringVar(&role, "role", "", "implements, tests, or proves")
	c.Flags().StringVar(&backendName, "backend", "go", "language backend")
	c.Flags().StringVar(&file, "file", "", "target binding file (derived from the requirement when empty)")
	return c
}

func unbindCmd() *cobra.Command {
	var req, symbol, role string
	c := &cobra.Command{
		Use:   "unbind",
		Short: "Remove binding claims",
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := author.ParseRole(role)
			if err != nil {
				return err
			}
			ups, removed, err := author.Unbind(os.DirFS(root), req, symbol, r)
			if err != nil {
				return err
			}
			if err := applyUpdates(root, ups); err != nil {
				return err
			}
			fmt.Println("removed", removed)
			return nil
		},
	}
	c.Flags().StringVar(&req, "req", "", "requirement identifier")
	c.Flags().StringVar(&symbol, "symbol", "", "narrow to one symbol")
	c.Flags().StringVar(&role, "role", "", "narrow to one role")
	return c
}

func gapCmd() *cobra.Command {
	var req, reason, coveredID, existsID, attested string
	c := &cobra.Command{
		Use:   "gap",
		Short: "Declare a coverage gap with a landing condition",
		RunE: func(cmd *cobra.Command, args []string) error {
			g := &stipulatorv1.Gap{}
			g.SetRequirementId(req)
			g.SetReason(reason)
			lc, err := author.NewLandingCondition(coveredID, existsID, attested)
			if err != nil {
				return err
			}
			if lc != nil {
				g.SetLands(lc)
			}
			up, err := author.Gap(os.DirFS(root), g)
			if err != nil {
				return err
			}
			return applyUpdates(root, []author.Update{*up})
		},
	}
	c.Flags().StringVar(&req, "req", "", "requirement identifier")
	c.Flags().StringVar(&reason, "reason", "", "why the gap exists")
	c.Flags().StringVar(&coveredID, "covered", "", "lands when this requirement is covered")
	c.Flags().StringVar(&existsID, "exists", "", "lands when this requirement exists")
	c.Flags().StringVar(&attested, "attested", "", "lands on this external condition, fired explicitly")
	return c
}

func diffCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "diff <old-root> <new-root>",
		Short: "Per-identity IR delta between two trees",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			oldSpec, err := mustCompile(args[0])
			if err != nil {
				return err
			}
			newSpec, err := mustCompile(args[1])
			if err != nil {
				return err
			}
			r := diff.Diff(oldSpec, newSpec)
			for _, line := range r.Lines() {
				fmt.Println(line)
			}
			if r.SemanticallyEmpty() {
				fmt.Println("no semantic delta")
			}
			return nil
		},
	}
}

func pinCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pin",
		Short: "Backfill binding content and shape pins",
		RunE: func(cmd *cobra.Command, args []string) error {
			spec, err := mustCompile(root)
			if err != nil {
				return err
			}
			store, err := records.Load(os.DirFS(root))
			if err != nil {
				return err
			}
			hashes := map[string]string{}
			for _, r := range spec.GetRequirements() {
				hashes[r.GetId()] = r.GetContentHash()
			}
			backends, err := makeBackends(root)
			if err != nil {
				return err
			}
			shapes := map[string]string{}
			for _, bf := range store.Bindings {
				for _, b := range bf.Set.GetBindings() {
					be, ok := backends[b.GetBackend()]
					if !ok {
						continue
					}
					res, shape, err := be.Resolve(b.GetSymbol())
					switch {
					case err != nil:
						fmt.Fprintf(os.Stderr, "pin: skipping %s: %v\n", b.GetSymbol(), err)
					case res == verify.Resolved:
						shapes[records.ShapeKey(b.GetBackend(), b.GetSymbol())] = shape
					}
				}
			}
			updates, err := records.Pin(store, hashes, shapes)
			if err != nil {
				return err
			}
			paths := make([]string, 0, len(updates))
			for p := range updates {
				paths = append(paths, p)
			}
			sort.Strings(paths)
			for _, p := range paths {
				if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(p)), updates[p], 0o644); err != nil {
					return err
				}
				fmt.Println("pinned", p)
			}
			if len(updates) == 0 {
				fmt.Println("all pins current")
			}
			return nil
		},
	}
}

func mustCompile(dir string) (*stipulatorv1.Spec, error) {
	spec, diags, err := compile.Compile(os.DirFS(dir))
	if err != nil {
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
