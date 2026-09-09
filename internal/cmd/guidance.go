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
func guidanceDoc() *guidancepkg.Document {
	doc, err := stipulator.GuidanceDocument()
	if err != nil {
		panic("cmd: embedded guidance document malformed: " + err.Error())
	}
	return doc
}

// guidanceShort and guidanceHelp are a command's served prose under
// its cli spelling, read from the guidance document at construction —
// never a second literal (REQ-mcp-guidance).
func guidanceShort(verb string) string {
	d, err := guidanceDoc().Description("cli", verb)
	if err != nil {
		panic("cmd: " + err.Error())
	}
	return d
}

// guidanceHelp is the knobless long rendering — cobra renders its
// own Flags: block, so the full knobs: block would print every knob
// twice in two wordings.
func guidanceHelp(verb string) string {
	l, err := guidanceDoc().Help("cli", verb)
	if err != nil {
		panic("cmd: " + err.Error())
	}
	return l
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
// the guidance document's knob text for that flag — the knob's terse
// first clause — so the usage strings are the document's rendering and
// never a second literal; a flag the document does not knob is a build
// defect the coverage judgment also refuses (REQ-mcp-guidance).
func renderKnobUsage(root *cobra.Command) {
	doc := guidanceDoc()
	var walk func(prefix string, c *cobra.Command)
	walk = func(prefix string, c *cobra.Command) {
		for _, child := range c.Commands() {
			if child.Hidden || child.Name() == "help" || child.Name() == "completion" {
				continue
			}
			name := strings.TrimSpace(prefix + " " + child.Name())
			if child.HasSubCommands() {
				walk(name, child)
				continue
			}
			child.LocalFlags().VisitAll(func(f *pflag.Flag) {
				if f.Name == "help" {
					return
				}
				k, err := doc.Knob("cli", name, f.Name)
				if err != nil {
					panic("cmd: " + err.Error())
				}
				f.Usage = stipulator.KnobClause(k.Text)
			})
		}
	}
	walk("", root)
}
