package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/encoding/prototext"
)

func compileCmd() *cobra.Command {
	var ir bool
	c := &cobra.Command{
		Use:   "compile",
		Short: "Compile the corpus; print diagnostics",
		RunE: func(cmd *cobra.Command, args []string) error {
			spec, err := mustCompile(chdir)
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
