// Command stipulator compiles and verifies a specification corpus.
//
//	stipulator compile [-C root] [-ir]   compile the corpus; print diagnostics
//	stipulator verify  [-C root]         check records against the corpus
//	stipulator pin     [-C root]         backfill binding content-hash pins
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/compile"
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
	ir := fs.Bool("ir", false, "print the compiled IR as textproto")

	switch os.Args[1] {
	case "compile":
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
		fs.Parse(os.Args[2:])
		spec := mustCompile(*root)
		store := mustLoad(*root)
		rep := verify.Run(spec, store)
		for _, p := range rep.Problems {
			fmt.Fprintln(os.Stderr, p)
		}
		fmt.Printf("bindings: %d pinned, %d stale; gaps: %d\n", rep.Pinned, rep.Stale, len(store.Gaps))
		if len(rep.Problems) > 0 {
			os.Exit(1)
		}
	case "pin":
		fs.Parse(os.Args[2:])
		spec := mustCompile(*root)
		store := mustLoad(*root)
		hashes := map[string]string{}
		for _, r := range spec.GetRequirements() {
			hashes[r.GetId()] = r.GetContentHash()
		}
		updates := records.Pin(store, hashes)
		paths := make([]string, 0, len(updates))
		for p := range updates {
			paths = append(paths, p)
		}
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
	fmt.Fprintln(os.Stderr, "usage: stipulator <compile|verify|pin> [-C root] [-ir]")
	os.Exit(2)
}
