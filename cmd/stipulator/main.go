// Command stipulator compiles and verifies a specification corpus.
//
//	stipulator compile [-C root] [-ir]   compile the corpus; print diagnostics
//	stipulator verify  [-C root] [-no-test]  check records against corpus and code
//	stipulator gate    [-C root]         coverage buckets + the gate verdict
//	stipulator diff <old-root> <new-root>  per-identity IR delta
//	stipulator pin     [-C root]         backfill binding content-hash pins
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/backends/golang"
	"github.com/greatliontech/stipulator/internal/compile"
	"github.com/greatliontech/stipulator/internal/coverage"
	"github.com/greatliontech/stipulator/internal/diff"
	"github.com/greatliontech/stipulator/internal/records"
	"github.com/greatliontech/stipulator/internal/verify"
	"google.golang.org/protobuf/encoding/prototext"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	fs := flag.NewFlagSet(os.Args[1], flag.ExitOnError)
	root := fs.String("C", ".", "repository root")
	// Verb-specific flags are registered per verb so help stays honest.
	var ir, noTest *bool

	switch os.Args[1] {
	case "compile":
		ir = fs.Bool("ir", false, "print the compiled IR as textproto")
		fs.Parse(os.Args[2:])
		spec := mustCompile(*root)
		if *ir {
			b, err := prototext.MarshalOptions{Multiline: true}.Marshal(spec)
			if err != nil {
				fatal(err)
			}
			os.Stdout.Write(b)
			return
		}
		fmt.Printf("ok: %d documents, %d requirements, %d terms, %d notes, %d annotations, %d edges\n",
			len(spec.GetDocuments()), len(spec.GetRequirements()), len(spec.GetTerms()),
			len(spec.GetNotes()), len(spec.GetAnnotations()), len(spec.GetEdges()))
	case "verify":
		noTest = fs.Bool("no-test", false, "skip running tests (no witnesses)")
		fs.Parse(os.Args[2:])
		spec := mustCompile(*root)
		store := mustLoad(*root)
		var testRun *verify.TestRun
		if !*noTest {
			tr, err := golang.RunTests(*root)
			if err != nil {
				fatal(err)
			}
			testRun = tr
		}
		rep := verify.Run(spec, store, mustBackends(*root), testRun)
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
		// verify fails only on verification errors; red evidence is bucket
		// data for the gate, which decides gap-excusability.
		if len(rep.Problems) > 0 {
			os.Exit(1)
		}
	case "diff":
		fs.Parse(os.Args[2:])
		args := fs.Args()
		if len(args) != 2 {
			fmt.Fprintln(os.Stderr, "usage: stipulator diff <old-root> <new-root>")
			os.Exit(2)
		}
		r := diff.Diff(mustCompile(args[0]), mustCompile(args[1]))
		for _, line := range r.Lines() {
			fmt.Println(line)
		}
		if r.SemanticallyEmpty() {
			fmt.Println("no semantic delta")
		}
	case "gate":
		fs.Parse(os.Args[2:])
		spec := mustCompile(*root)
		store := mustLoad(*root)
		testRun, err := golang.RunTests(*root)
		if err != nil {
			fatal(err)
		}
		rep := verify.Run(spec, store, mustBackends(*root), testRun)
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
	case "pin":
		fs.Parse(os.Args[2:])
		spec := mustCompile(*root)
		store := mustLoad(*root)
		hashes := map[string]string{}
		for _, r := range spec.GetRequirements() {
			hashes[r.GetId()] = r.GetContentHash()
		}
		shapes := map[string]string{}
		backends := mustBackends(*root)
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
			fatal(err)
		}
		paths := make([]string, 0, len(updates))
		for p := range updates {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		for _, p := range paths {
			if err := os.WriteFile(filepath.Join(*root, filepath.FromSlash(p)), updates[p], 0o644); err != nil {
				fatal(err)
			}
			fmt.Println("pinned", p)
		}
		if len(updates) == 0 {
			fmt.Println("all pins current")
		}
	default:
		usage()
	}
}

func mustCompile(root string) *stipulatorv1.Spec {
	spec, diags, err := compile.Compile(os.DirFS(root))
	if err != nil {
		fatal(err)
	}
	if len(diags) > 0 {
		for _, d := range diags {
			fmt.Fprintln(os.Stderr, d)
		}
		os.Exit(1)
	}
	return spec
}

func mustBackends(root string) map[string]verify.Backend {
	gb, err := golang.New(root)
	if err != nil {
		fatal(err)
	}
	return map[string]verify.Backend{"go": gb}
}

func mustLoad(root string) *records.Store {
	store, err := records.Load(os.DirFS(root))
	if err != nil {
		fatal(err)
	}
	return store
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "stipulator:", err)
	os.Exit(2)
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: stipulator <compile|verify|gate|pin> [flags] | stipulator diff <old-root> <new-root>")
	os.Exit(2)
}
