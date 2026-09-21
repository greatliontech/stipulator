package cmd

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/greatliontech/gofresh/guidance"
	stipulator "github.com/greatliontech/stipulator"
	"github.com/greatliontech/stipulator/internal/policy"
	"github.com/greatliontech/stipulator/stipulate"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// The CLI surface and the guidance document cannot drift: every
// visible leaf command's spelling and every local flag is documented,
// in both directions, judged over the real cobra tree
// (REQ-mcp-guidance). Grouping parents and the root-persistent chdir
// flag are surface plumbing, not verbs; cobra's help and completion
// commands join the tree only at execution, after the walk.
//
//gofresh:pure
func TestGuidanceCoversTheCLISurface(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-guidance")
	doc, err := stipulator.GuidanceDocument()
	if err != nil {
		t.Fatal(err)
	}
	registered := map[string]guidance.Registered{}
	var walk func(prefix string, c *cobra.Command)
	walk = func(prefix string, c *cobra.Command) {
		for _, child := range c.Commands() {
			name := strings.TrimSpace(prefix + " " + child.Name())
			if child.HasSubCommands() {
				walk(name, child)
				continue
			}
			flags := guidance.Registered{}
			child.LocalFlags().VisitAll(func(f *pflag.Flag) {
				// The registration carries the non-zero-default fact the
				// CLI lint judges: a flag cobra prints a default for must
				// not spell one in its knob prose.
				flags[f.Name] = !zeroDefault(f)
				// The usage string is gofresh's usage projection of the
				// document's knob — the clause in pflag's grammar, derived
				// here independently — identity, never a name match.
				k, err := doc.Knob("cli", name, f.Name)
				if err != nil {
					t.Errorf("%s --%s: %v", name, f.Name, err)
					return
				}
				if want := cliUsage(firstClause(k.Text)); f.Usage != want || f.Usage == "" {
					t.Errorf("%s --%s usage %q diverged from the document's %q", name, f.Name, f.Usage, want)
				}
				if name == "verify" && f.Name == "req" && f.Usage != "requirement identifiers to scope the report to (comma-separated on mcp; repeatable on the cli)" {
					t.Errorf("verify --req usage = %q; want the parenthesis kept whole", f.Usage)
				}
				if name == "verify" && f.Name == "no-test" && f.Usage != "the records-only judgment: no witness run, no policy capture" {
					t.Errorf("verify --no-test usage = %q; want the document's first clause", f.Usage)
				}
				// pflag's grammar: a code span unquoted (a quoted span
				// would name the flag's value), a knob on a zero-default
				// flag spelling its absence in prose, never in the
				// parenthetical cobra prints for a non-zero one.
				if name == "bind" && f.Name == "clause" && strings.Contains(f.Usage, "`") {
					t.Errorf("bind --clause usage = %q; want the code span unquoted", f.Usage)
				}
				if name == "retarget" && f.Name == "backend" && f.Usage != "backend whose symbols retarget (go where absent; taken once, repetition refused)" {
					t.Errorf("retarget --backend usage = %q; want the clause with its absence prose", f.Usage)
				}
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
		// The cobra Long is the knobless help rendering — cobra's own
		// Flags: block carries the knob list on this surface — and, for
		// a knobbed verb, the pointer to the knobs' whole prose after it
		// (the served path: `stipulator guidance <verb>`, a two-word
		// verb quoted); a knobless verb keeps the help alone or none.
		help, err := doc.Help("cli", name)
		if err != nil {
			t.Errorf("%q: %v", name, err)
			continue
		}
		if c.HasLocalFlags() {
			want := help + "\n\nThe knobs' whole prose: stipulator guidance " + shellSpelling(name) + "."
			if c.Long != want {
				t.Errorf("%q Long diverged from Help + pointer:\ncli %q\nwant %q", name, c.Long, want)
			}
		} else if c.Long != "" && c.Long != help {
			t.Errorf("%q Long diverged from Help:\ncli %q\ndoc %q", name, c.Long, help)
		}
		if strings.Contains(c.Long, "\nknobs:") {
			t.Errorf("%q Long carries the knobs block beside cobra's Flags", name)
		}
	}
	// The policy record path in the served guidance is the code's own
	// constant — the document must not become a second, driftable home
	// for it.
	long, err := doc.Help("cli", "policy init")
	if err != nil || !strings.Contains(long, policy.Path) {
		t.Errorf("policy init served help does not carry policy.Path (%v): %q", err, long)
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

// firstClause is the test's own reading of the clause rule: the text up
// to the first semicolon outside parentheses — independent of the
// rendering it judges — its trailing period trimmed as the rendering trims it.
//
//gofresh:pure
func firstClause(text string) string {
	depth := 0
	for i := 0; i < len(text); i++ {
		switch text[i] {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case ';':
			if depth == 0 {
				return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(text[:i]), "."))
			}
		}
	}
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(text), "."))
}

// TestGuidanceRefusalsCarryThePackagesWording pins the refusal a face's
// construction meets for a knob or a verb the document does not carry:
// the package's wording, one on both faces (REQ-mcp-guidance).
//
//gofresh:pure
func TestGuidanceRefusalsCarryThePackagesWording(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-guidance")
	refusal := func(f func()) (msg string) {
		defer func() { msg = fmt.Sprint(recover()) }()
		f()
		return ""
	}
	for name, f := range map[string]func(){
		"cli knob":  func() { stipulator.GuidanceKnob("cli", "verify", "nosuch") },
		"cli verb":  func() { stipulator.GuidanceRegistration("cli", "nosuch") },
		"wire knob": func() { stipulator.GuidanceKnob("mcp", "verify", "nosuch") },
		"wire verb": func() { stipulator.GuidanceRegistration("mcp", "nosuch") },
	} {
		if msg := refusal(f); !strings.HasPrefix(msg, "stipulator: guidance: ") {
			t.Fatalf("%s refusal = %q, want the package's wording", name, msg)
		}
	}
}

// zeroDefault is pflag's own per-type zero: the defaults cobra prints
// beside a usage are exactly the non-zero ones, per flag type (a string
// "0" prints; a bool false does not).
//
//gofresh:pure
func zeroDefault(f *pflag.Flag) bool {
	switch f.Value.Type() {
	case "string":
		return f.DefValue == ""
	case "bool":
		return f.DefValue == "false"
	case "int", "int64":
		return f.DefValue == "0"
	case "duration":
		return f.DefValue == "0" || f.DefValue == "0s"
	case "stringArray", "stringSlice":
		return f.DefValue == "[]"
	}
	return f.DefValue == ""
}

// cliUsage is the test's own reading of pflag's usage grammar over a
// clause: code spans unquoted and a trailing default parenthetical —
// the one cobra prints itself — dropped; the served string is judged
// against this derivation, never against the package's projection.
//
//gofresh:pure
func cliUsage(clause string) string {
	clause = strings.ReplaceAll(clause, "`", "")
	if i := strings.LastIndex(clause, " (default "); i >= 0 && strings.HasSuffix(clause, ")") {
		clause = clause[:i]
	}
	return clause
}

// shellSpelling is the test's own reading of the pointer's verb
// spelling: a verb with a space is quoted for the shell.
//
//gofresh:pure
func shellSpelling(verb string) string {
	if strings.ContainsAny(verb, " \t") {
		return "\"" + verb + "\""
	}
	return verb
}
