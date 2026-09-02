package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/greatliontech/stipulator/internal/author"
)

func bindCmd() *cobra.Command {
	var reqs, symbols, roles, backendNames, files []string
	c := &cobra.Command{
		Use:   "bind",
		Short: guidanceShort("bind"),
		RunE: func(cmd *cobra.Command, args []string) error {
			claims, err := bindClaims(reqs, symbols, roles, backendNames, files)
			if err != nil {
				return err
			}
			backends, err := makeBackends(cmd.Context(), chdir)
			if err != nil {
				return err
			}
			ups, err := author.Binds(os.DirFS(chdir), backends, claims)
			if err != nil {
				return err
			}
			return applyUpdates(chdir, ups)
		},
	}
	c.Flags().StringArrayVar(&reqs, "req", nil, "requirement identifier (each repetition starts a claim)")
	c.Flags().StringArrayVar(&symbols, "symbol", nil, "backend-scoped symbol reference (one per claim)")
	c.Flags().StringArrayVar(&roles, "role", nil, "implements, tests, or proves (once for all claims, or one per claim)")
	c.Flags().StringArrayVar(&backendNames, "backend", nil, "language backend (default go; once for all claims, or one per claim)")
	c.Flags().StringArrayVar(&files, "file", nil, "target binding file (derived from the requirement when empty; once for all claims, or one per claim)")
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
func bindClaims(reqs, symbols, roles, backendNames, files []string) ([]author.BindRequest, error) {
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
	claims := make([]author.BindRequest, 0, n)
	for i := range reqs {
		r, err := author.ParseRole(role(i))
		if err != nil {
			return nil, fmt.Errorf("claim %d (%s): %w", i+1, reqs[i], err)
		}
		claims = append(claims, author.BindRequest{
			Requirement: reqs[i], Symbol: symbols[i], Backend: backend(i),
			Role: r, File: file(i),
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
	var reqs, symbols, roles []string
	c := &cobra.Command{
		Use:   "unbind",
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
	c.Flags().StringArrayVar(&reqs, "req", nil, "requirement identifier")
	c.Flags().StringArrayVar(&symbols, "symbol", nil, "narrow to one symbol")
	c.Flags().StringArrayVar(&roles, "role", nil, "narrow to one role")
	registerReqCompletions(c, "req")
	_ = c.RegisterFlagCompletionFunc("role", completeRoles)
	return c
}
