package cmd

import (
	"bytes"
	"strings"
	"testing"

	stipulator "github.com/greatliontech/stipulator"
	"github.com/greatliontech/stipulator/internal/policy"
	"github.com/greatliontech/stipulator/stipulate"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// The CLI surface and the guidance document cannot drift: every
// visible leaf command's spelling and every local flag is documented,
// in both directions, judged over the real cobra tree
// (REQ-mcp-guidance). Grouping parents, cobra's own help/completion
// plumbing, the root-persistent chdir flag, and the hidden internal
// resolver are surface plumbing, not verbs.
//
//gofresh:pure
func TestGuidanceCoversTheCLISurface(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-guidance")
	doc, err := stipulator.GuidanceDocument()
	if err != nil {
		t.Fatal(err)
	}
	registered := map[string][]string{}
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
			var flags []string
			child.LocalFlags().VisitAll(func(f *pflag.Flag) {
				if f.Name == "help" {
					return
				}
				flags = append(flags, f.Name)
			})
			registered[name] = flags
		}
	}
	walk("", newRootCmd())
	defects, err := doc.Coverage("cli", registered)
	if err != nil || len(defects) != 0 {
		t.Fatalf("cli coverage: err=%v defects:\n%s", err, strings.Join(defects, "\n"))
	}
	// Served Short and Long ARE the document's projections — identity,
	// not resemblance.
	root := newRootCmd()
	for name := range registered {
		c, _, err := root.Find(strings.Fields(name))
		if err != nil {
			t.Fatalf("find %q: %v", name, err)
		}
		short, err := doc.Description("cli", name)
		if err != nil {
			t.Errorf("%q: %v", name, err)
			continue
		}
		if c.Short != short {
			t.Errorf("%q Short diverged:\ncli %q\ndoc %q", name, c.Short, short)
		}
		if c.Long != "" {
			long, err := doc.Long("cli", name)
			if err != nil || c.Long != long {
				t.Errorf("%q Long diverged (err=%v):\ncli %q\ndoc %q", name, err, c.Long, long)
			}
		}
	}
	// The policy record path in the served guidance is the code's own
	// constant — the document must not become a second, driftable home
	// for it.
	long, err := doc.Long("cli", "policy init")
	if err != nil || !strings.Contains(long, policy.Path) {
		t.Errorf("policy init guidance does not carry policy.Path (%v): %q", err, long)
	}
}

// The guidance command serves the document under cli spellings: a
// verb's long section, the decision map for no verb, and a teaching
// error for an unknown one (REQ-mcp-guidance). It runs outside a
// corpus — the document is embedded.
//
//gofresh:pure
func TestGuidanceCommandServesTheDocument(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-guidance")
	doc, err := stipulator.GuidanceDocument()
	if err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) (string, error) {
		t.Helper()
		root := newRootCmd()
		var out bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&out)
		root.SetArgs(args)
		err := root.Execute()
		return out.String(), err
	}
	dir := t.TempDir() // no corpus here
	got, err := run("-C", dir, "guidance", "check")
	if err != nil {
		t.Fatal(err)
	}
	long, _ := doc.Long("cli", "check")
	if strings.TrimSuffix(got, "\n") != long {
		t.Fatalf("guidance check diverged:\n%q\nwant\n%q", got, long)
	}
	got, err = run("-C", dir, "guidance")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSuffix(got, "\n") != doc.Orientation() {
		t.Fatalf("guidance orientation diverged: %q", got)
	}
	if _, err = run("-C", dir, "guidance", "vanished"); err == nil || !strings.Contains(err.Error(), "decision map") {
		t.Fatalf("unknown verb: err = %v", err)
	}
}
