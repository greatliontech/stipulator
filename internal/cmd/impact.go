package cmd

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/impact"
)

func impactCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "impact",
		Short: "Preview what the working-tree change set plausibly touches",
		Long: "Joins the worktree-vs-HEAD change set with the corpus and the committed\n" +
			"bindings: which requirements' spec content moved, which bindings' symbols\n" +
			"declare in changed files, and which witness subjects the change reaches\n" +
			"through the import graph and embed couplings. Executes nothing and claims no freshness\n" +
			"verdict — the preview names candidates for the witnessed surfaces\n" +
			"(check, verify) to decide, and an empty preview is advisory, never\n" +
			"proof of no impact: reach through non-import couplings is invisible here.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := impact.Preview(cmd.Context(), chdir)
			if err != nil {
				return err
			}
			renderImpact(os.Stdout, r)
			return nil
		},
	}
	return c
}

// renderImpact prints the preview, one deterministic section per claim
// class. Every line is a candidate, never a verdict. Quiescence needs
// every section empty: a spec delta can exist with an empty change set
// (a gitignored spec document compiles but never reaches the VCS status),
// and it must render, not vanish behind "no changes".
func renderImpact(w io.Writer, r *impact.Report) {
	if len(r.Changed) == 0 && r.Spec.SemanticallyEmpty() && len(r.Bound) == 0 && len(r.Witnesses) == 0 {
		fmt.Fprintln(w, "no changes against HEAD")
		return
	}
	noun := "files"
	if len(r.Changed) == 1 {
		noun = "file"
	}
	fmt.Fprintf(w, "changed: %d %s against HEAD\n", len(r.Changed), noun)
	if r.Spec.SemanticallyEmpty() {
		fmt.Fprintln(w, dim("spec: no semantic delta"))
	} else {
		for _, line := range r.Spec.Lines() {
			fmt.Fprintln(w, "spec: "+line)
		}
	}
	for _, h := range r.Bound {
		fmt.Fprintf(w, "bound: %s  %s  %s  (%s)\n", h.Requirement, roleWord(h.Role), h.Symbol, h.File)
	}
	for _, h := range r.Witnesses {
		fmt.Fprintf(w, "witness reached: %s  %s\n", h.Requirement, h.Symbol)
	}
	if r.Unconsulted > 0 {
		noun := "bindings"
		if r.Unconsulted == 1 {
			noun = "binding"
		}
		fmt.Fprintln(w, dim(fmt.Sprintf("%d %s on backends the preview does not consult", r.Unconsulted, noun)))
	}
	fmt.Fprintln(w, dim("preview only — the witnessed surfaces decide; empty is advisory, not proof"))
}

func roleWord(role stipulatorv1.BindingRole) string {
	switch role {
	case stipulatorv1.BindingRole_BINDING_ROLE_IMPLEMENTS:
		return "implements"
	case stipulatorv1.BindingRole_BINDING_ROLE_TESTS:
		return "tests"
	case stipulatorv1.BindingRole_BINDING_ROLE_PROVES:
		return "proves"
	}
	return "unspecified"
}
