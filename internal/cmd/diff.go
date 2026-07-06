package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/diff"
	"github.com/greatliontech/stipulator/internal/gitfs"
)

func diffCmd() *cobra.Command {
	var against string
	c := &cobra.Command{
		Use:   "diff [<old-root> <new-root>]",
		Short: "Per-identity IR delta between two trees, or against a git revision",
		Long: "Two forms: `diff <old-root> <new-root>` compares two checked-out trees;\n" +
			"`diff --against <rev>` compares the corpus as committed at the revision\n" +
			"(read straight from the object store — no checkout) with the working tree.",
		Args: cobra.RangeArgs(0, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			var oldSpec, newSpec *stipulatorv1.Spec
			var err error
			switch {
			case against != "" && len(args) == 0:
				oldSpec, err = compileRevision(chdir, against)
				if err != nil {
					return err
				}
				newSpec, err = mustCompile(chdir)
				if err != nil {
					return err
				}
			case against == "" && len(args) == 2:
				oldSpec, err = mustCompile(args[0])
				if err != nil {
					return err
				}
				newSpec, err = mustCompile(args[1])
				if err != nil {
					return err
				}
			default:
				return fmt.Errorf("give either two roots or --against <rev>")
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
	c.Flags().StringVar(&against, "against", "", "git revision holding the old corpus (HEAD~1, branch, tag, hash)")
	return c
}

// compileRevision compiles the corpus as committed at rev, from the
// repository's object store.
func compileRevision(dir, rev string) (*stipulatorv1.Spec, error) {
	fsys, err := gitfs.FS(dir, rev)
	if err != nil {
		return nil, err
	}
	return mustCompileFS(fsys, "revision "+rev)
}
