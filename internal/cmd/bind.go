package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/greatliontech/stipulator/internal/author"
	"github.com/greatliontech/stipulator/internal/remedy"
)

func bindCmd() *cobra.Command {
	var reqs, symbols, roles, backendNames, files, clauses []string
	c := &cobra.Command{
		Use:   remedy.VerbBind,
		Short: guidanceShort("bind"),
		RunE: func(cmd *cobra.Command, args []string) error {
			claims, err := bindClaims(reqs, symbols, roles, backendNames, files, clauses)
			if err != nil {
				return err
			}
			backends, closeBackends, err := makeBackends(cmd.Context(), chdir)
			if err != nil {
				return err
			}
			defer closeBackends()
			ups, err := author.Binds(os.DirFS(chdir), backends, claims)
			if err != nil {
				return err
			}
			return applyUpdates(chdir, ups)
		},
	}
	c.Flags().StringArrayVar(&reqs, remedy.FlagReq, nil, "")
	c.Flags().StringArrayVar(&symbols, remedy.FlagSymbol, nil, "")
	c.Flags().StringArrayVar(&roles, remedy.FlagRole, nil, "")
	c.Flags().StringArrayVar(&backendNames, "backend", nil, "")
	c.Flags().StringArrayVar(&files, "file", nil, "")
	c.Flags().StringArrayVar(&clauses, remedy.FlagClause, nil, "")
	registerReqCompletions(c, "req")
	_ = c.RegisterFlagCompletionFunc("role", completeRoles)
	_ = c.RegisterFlagCompletionFunc("backend", completeBackends)
	return c
}

// bindClaims aligns the repeated flag groups into a claims batch: one
// claim per --req, exactly one --symbol per claim, and --role,
// --backend, and --file given either once (applying to every claim) or
// exactly once per claim. Any other count refuses with both counts
// named — a silent alignment would drop or misassign a claim the
// caller expressed, the accept-and-drop this surface forbids
// (REQ-evidence-claim-batch).
func bindClaims(reqs, symbols, roles, backendNames, files, clauses []string) ([]author.BindRequest, error) {
	n := len(reqs)
	if n == 0 {
		return nil, fmt.Errorf("at least one --req is required")
	}
	if len(symbols) != n {
		return nil, fmt.Errorf("%d --req flag(s) with %d --symbol flag(s): each claim needs exactly one symbol", n, len(symbols))
	}
	pick := func(name string, vals []string, def string) (func(int) string, error) {
		switch len(vals) {
		case 0:
			return func(int) string { return def }, nil
		case 1:
			return func(int) string { return vals[0] }, nil
		case n:
			return func(i int) string { return vals[i] }, nil
		}
		return nil, fmt.Errorf("%d --%s flag(s) for %d claim(s): give one (applying to every claim) or exactly one per claim", len(vals), name, n)
	}
	role, err := pick("role", roles, "")
	if err != nil {
		return nil, err
	}
	backend, err := pick("backend", backendNames, "go")
	if err != nil {
		return nil, err
	}
	file, err := pick("file", files, "")
	if err != nil {
		return nil, err
	}
	// A clause names one claim's scope, never every claim's: given at
	// all, it is given exactly once per claim (an empty entry keeps
	// that claim whole).
	if len(clauses) != 0 && len(clauses) != n {
		return nil, fmt.Errorf("%d --clause flag(s) for %d claim(s): give exactly one per claim (empty for a whole-requirement claim), or none", len(clauses), n)
	}
	clause := func(i int) string {
		if len(clauses) == 0 {
			return ""
		}
		return clauses[i]
	}
	claims := make([]author.BindRequest, 0, n)
	for i := range reqs {
		r, err := author.ParseRole(role(i))
		if err != nil {
			return nil, fmt.Errorf("claim %d (%s): %w", i+1, reqs[i], err)
		}
		claims = append(claims, author.BindRequest{
			Requirement: reqs[i], Symbol: symbols[i], Backend: backend(i),
			Role: r, File: file(i), Clause: clause(i),
		})
	}
	return claims, nil
}

// oneFlag resolves a flag its verb takes exactly once: a repetition
// expresses a batch the verb does not form from that flag, so it
// refuses loudly rather than keeping the last value — a value the
// caller expressed is never silently dropped
// (REQ-evidence-claim-batch's refuse arm). Every claim-writing verb's
// single-value flags route through this one helper, so no verb can
// drift back to last-wins.
func oneFlag(name string, vals []string) (string, error) {
	switch len(vals) {
	case 0:
		return "", nil
	case 1:
		return vals[0], nil
	}
	return "", fmt.Errorf("--%s given %d times: it applies once here and forms no batch", name, len(vals))
}

func unbindCmd() *cobra.Command {
	var reqs, symbols, roles, clauses []string
	c := &cobra.Command{
		Use:   remedy.VerbUnbind,
		Short: guidanceShort("unbind"),
		RunE: func(cmd *cobra.Command, args []string) error {
			req, err := oneFlag("req", reqs)
			if err != nil {
				return err
			}
			symbol, err := oneFlag("symbol", symbols)
			if err != nil {
				return err
			}
			roleWord, err := oneFlag("role", roles)
			if err != nil {
				return err
			}
			r, err := author.ParseRole(roleWord)
			if err != nil {
				return err
			}
			clause, err := oneFlag("clause", clauses)
			if err != nil {
				return err
			}
			ups, removed, err := author.Unbind(os.DirFS(chdir), req, symbol, r, clause)
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
	c.Flags().StringArrayVar(&reqs, remedy.FlagReq, nil, "")
	c.Flags().StringArrayVar(&symbols, remedy.FlagSymbol, nil, "")
	c.Flags().StringArrayVar(&clauses, remedy.FlagClause, nil, "")
	c.Flags().StringArrayVar(&roles, remedy.FlagRole, nil, "")
	registerReqCompletions(c, "req")
	_ = c.RegisterFlagCompletionFunc("role", completeRoles)
	return c
}
