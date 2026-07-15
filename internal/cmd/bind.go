package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/greatliontech/stipulator/internal/author"
)

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
			backends, err := makeBackends(cmd.Context(), chdir)
			if err != nil {
				return err
			}
			up, err := author.Bind(os.DirFS(chdir), backends, author.BindRequest{
				Requirement: req, Symbol: symbol, Backend: backendName,
				Role: r, File: file,
			})
			if err != nil {
				return err
			}
			return applyUpdates(chdir, []author.Update{*up})
		},
	}
	c.Flags().StringVar(&req, "req", "", "requirement identifier")
	c.Flags().StringVar(&symbol, "symbol", "", "backend-scoped symbol reference")
	c.Flags().StringVar(&role, "role", "", "implements, tests, or proves")
	c.Flags().StringVar(&backendName, "backend", "go", "language backend")
	c.Flags().StringVar(&file, "file", "", "target binding file (derived from the requirement when empty)")
	registerReqCompletions(c, "req")
	_ = c.RegisterFlagCompletionFunc("role", completeRoles)
	_ = c.RegisterFlagCompletionFunc("backend", completeBackends)
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
			ups, removed, err := author.Unbind(os.DirFS(chdir), req, symbol, r)
			if err != nil {
				return err
			}
			if err := applyUpdates(chdir, ups); err != nil {
				return err
			}
			fmt.Println("removed", removed)
			return nil
		},
	}
	c.Flags().StringVar(&req, "req", "", "requirement identifier")
	c.Flags().StringVar(&symbol, "symbol", "", "narrow to one symbol")
	c.Flags().StringVar(&role, "role", "", "narrow to one role")
	registerReqCompletions(c, "req")
	_ = c.RegisterFlagCompletionFunc("role", completeRoles)
	return c
}
