package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/greatliontech/stipulator/internal/diff"
)

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
