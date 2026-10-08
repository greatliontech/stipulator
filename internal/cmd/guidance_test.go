package cmd

import (
	"bytes"
	"fmt"
	"regexp"
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
			served := map[string]string{}
			child.LocalFlags().VisitAll(func(f *pflag.Flag) {
				// The registration carries the non-zero-default fact the
				// CLI lint judges — the fleet's rule — and the fact is
				// pflag's own: cobra prints a default beside the usage
				// exactly when the rule says so.
				flags[f.Name] = guidance.PrintsDefault(f.Value.Type(), f.DefValue)
				alone := pflag.NewFlagSet(f.Name, pflag.ContinueOnError)
				copied := *f
				alone.AddFlag(&copied)
				if printed := strings.Contains(alone.FlagUsages(), "(default "); printed != flags[f.Name] {
					t.Errorf("%s --%s: pflag prints a default = %v, the fleet's rule says %v", name, f.Name, printed, flags[f.Name])
				}
				// The usage string is judged as served bytes against the
				// document's projection (Document.Served, below) — never
				// through a grammar of this test's own; the literal
				// anchors are the independence belt.
				served[f.Name] = f.Usage
				if f.Usage == "" {
					t.Errorf("%s --%s serves an empty usage", name, f.Name)
				}
				// A terse clause names no face: the per-face form follows
				// the first semicolon, where the whole prose keeps it
				// (REQ-mcp-guidance).
				if faceWord.MatchString(f.Usage) {
					t.Errorf("%s --%s usage %q names a face", name, f.Name, f.Usage)
				}
				if name == "verify" && f.Name == "req" && f.Usage != "comma-separated requirement identifiers to scope the report to" {
					t.Errorf("verify --req usage = %q; want the face-neutral clause stating the list form", f.Usage)
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
					t.Errorf("retarget --backend usage = %q; want the clause with its absence prose, the parenthesis kept whole", f.Usage)
				}
			})
			if defects, err := doc.Served("cli", name, served); err != nil || len(defects) != 0 {
				t.Errorf("%s: served usages: err=%v defects:\n%s", name, err, strings.Join(defects, "\n"))
			}
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

// faceWord matches a served string naming a face — the clause rule's
// universal, refused on both faces.
var faceWord = regexp.MustCompile(`\b(?:mcp|cli)\b`)

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
