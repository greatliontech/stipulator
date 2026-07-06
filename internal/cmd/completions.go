package cmd

import (
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/greatliontech/stipulator/internal/compile"
)

// completeReqs offers requirement identifiers from the compiled corpus.
// Compilation is milliseconds, so completion stays live with the spec.
func completeReqs(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	spec, diags, err := compile.Compile(os.DirFS(chdir))
	if err != nil || len(compile.Errors(diags)) > 0 {
		return nil, cobra.ShellCompDirectiveError
	}
	var out []string
	for _, r := range spec.GetRequirements() {
		if strings.HasPrefix(r.GetId(), toComplete) {
			out = append(out, r.GetId()+"\t"+truncate(r.GetText(), 48))
		}
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

func completeRoles(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	return []string{"implements", "tests", "proves"}, cobra.ShellCompDirectiveNoFileComp
}

func completeBackends(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	return []string{"go", "proto"}, cobra.ShellCompDirectiveNoFileComp
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// registerReqCompletions wires identifier completion onto the named flags.
func registerReqCompletions(c *cobra.Command, flags ...string) {
	for _, f := range flags {
		_ = c.RegisterFlagCompletionFunc(f, completeReqs)
	}
}
