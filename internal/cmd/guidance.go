package cmd

import (
	"fmt"
	"strings"

	guidancepkg "github.com/greatliontech/gofresh/guidance"
	stipulator "github.com/greatliontech/stipulator"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// guidanceDoc is the embedded guidance document; a malformed document
// is a build defect the parse-pinning test surfaces, so command
// construction fails loudly rather than serving nothing.
func guidanceDoc() *guidancepkg.Document { return stipulator.Guidance() }

// guidanceShort and guidanceHelp are a command's served prose under
// its cli spelling, the registration's purpose and knobless help read
// at construction — never a second literal, a verb the document does
// not carry refusing with the package's wording (REQ-mcp-guidance).
// A knobbed verb's long help is rendered whole by renderKnobUsage; a
// knobless verb's constructor sets it here.
func guidanceShort(verb string) string {
	return stipulator.GuidanceRegistration("cli", verb).Description
}

// guidanceHelp is the knobless long rendering — cobra renders its
// own Flags: block, so the full knobs: block would print every knob
// twice in two wordings.
func guidanceHelp(verb string) string {
	return stipulator.GuidanceRegistration("cli", verb).Help
}

// guidanceCmd serves the guidance document itself: a verb's full
// section, or the decision map for orientation. It works outside a
// corpus — the document is embedded.
func guidanceCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "guidance [verb]",
		Short: guidanceShort("guidance"),
		Long:  guidanceHelp("guidance"),
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), guidanceDoc().Orientation())
				return nil
			}
			long, err := guidanceDoc().Long("cli", args[0])
			if err != nil {
				return fmt.Errorf("%w; run guidance with no verb for the decision map, which names every verb", err)
			}
			fmt.Fprintln(cmd.OutOrStdout(), long)
			return nil
		},
	}
}

// renderKnobUsage sets every visible leaf command's local flag usage to
// gofresh's usage projection of the guidance document's knob for that
// flag — the terse clause in pflag's grammar — and renders a knobbed
// verb's long help as the registration's knobless help followed by its
// prose pointer (cobra's own Flags: block carries the knob list, the
// pointer names the knobs' whole prose), so the served strings are the
// document's rendering and never a second literal; a flag the document
// does not knob refuses construction with the package's wording, and
// the coverage judgment refuses it too (REQ-mcp-guidance).
func renderKnobUsage(root *cobra.Command) {
	var walk func(prefix string, c *cobra.Command)
	walk = func(prefix string, c *cobra.Command) {
		// The walk runs over a fresh root, before cobra adds its help and
		// completion commands and each command's --help flag at
		// execution, so every child is a verb or a grouping parent and
		// every local flag a knob.
		for _, child := range c.Commands() {
			name := strings.TrimSpace(prefix + " " + child.Name())
			if child.HasSubCommands() {
				walk(name, child)
				continue
			}
			knobbed := false
			child.LocalFlags().VisitAll(func(f *pflag.Flag) {
				knobbed = true
				f.Usage = stipulator.GuidanceKnob("cli", name, f.Name).Usage()
			})
			if knobbed {
				registration := stipulator.GuidanceRegistration("cli", name)
				child.Long = registration.Help + "\n\n" + registration.ProsePointer
			}
		}
	}
	walk("", root)
}
