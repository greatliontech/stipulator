// Package remedy is the one spelling source of the tool's executable
// remedies: the verb and flag names the command tree registers, and the
// composers every rendered finding names its repair with
// (REQ-change-remediation). The command constructors read the same
// names, so a renamed verb or flag moves the remedy with it, and a test
// parses every composition against the command tree — a spelling that
// does not parse there is a build defect, never a served one.
package remedy

import (
	"sort"
	"strings"
)

// The verbs, in the CLI's spelling.
const (
	Tool                  = "stipulator"
	VerbPin               = "pin"
	VerbBind              = "bind"
	VerbUnbind            = "unbind"
	VerbAttest            = "attest"
	VerbAttestRequirement = "requirement"
	VerbGap               = "gap"
	VerbPrune             = "prune"
	VerbDispose           = "dispose"
	VerbDisposeRetire     = "retire"
	VerbDisposeSupersede  = "supersede"
	VerbRetarget          = "retarget"
	VerbInit              = "init"
	VerbCompile           = "compile"
	VerbVerify            = "verify"
	VerbPolicy            = "policy"
)

// The flags, without their dashes.
const (
	FlagReq      = "req"
	FlagSymbol   = "symbol"
	FlagClause   = "clause"
	FlagRole     = "role"
	FlagRetract  = "retract"
	FlagID       = "id"
	FlagFrom     = "from"
	FlagInto     = "into"
	FlagForce    = "force"
	FlagDangling = "dangling"
)

func flag(name string) string { return "--" + name }

func compose(parts ...string) string { return strings.Join(parts, " ") }

// Pin is the editorial re-consent of the named requirements, or the
// blanket re-pin when none is named.
func Pin(ids ...string) string {
	parts := []string{Tool, VerbPin}
	for _, id := range ids {
		parts = append(parts, flag(FlagReq), id)
	}
	return compose(parts...)
}

// Unbind removes one binding; the clause rides only when the claim
// names one.
func Unbind(req, symbol, clause string) string {
	parts := []string{Tool, VerbUnbind, flag(FlagReq), req}
	if symbol != "" {
		parts = append(parts, flag(FlagSymbol), symbol)
	}
	if clause != "" {
		parts = append(parts, flag(FlagClause), clause)
	}
	return compose(parts...)
}

// Bind records one claim of the given role on the named symbol.
func Bind(req, role, symbol string) string {
	return compose(Tool, VerbBind, flag(FlagReq), req, flag(FlagRole), role, flag(FlagSymbol), symbol)
}

// Retire tombstones a removed identity.
func Retire(id string) string {
	return compose(Tool, VerbDispose, VerbDisposeRetire, flag(FlagID), id)
}

// Supersede tombstones the sources and accepts the declared edges into
// the successors; force lifts the typo guard for a source no record
// names.
func Supersede(from, into []string, force bool) string {
	parts := []string{Tool, VerbDispose, VerbDisposeSupersede, flag(FlagFrom), strings.Join(from, ","), flag(FlagInto), strings.Join(into, ",")}
	if force {
		parts = append(parts, flag(FlagForce))
	}
	return compose(parts...)
}

// AttestRequirement authors or refreshes the requirement's judgment.
func AttestRequirement(req string) string {
	return compose(Tool, VerbAttest, VerbAttestRequirement, flag(FlagReq), req)
}

// AttestRetract withdraws the requirement's judgment.
func AttestRetract(req string) string {
	return compose(AttestRequirement(req), flag(FlagRetract))
}

// GapRetract deletes the requirement's gap records.
func GapRetract(req string) string {
	return compose(Tool, VerbGap, flag(FlagReq), req, flag(FlagRetract))
}

// Prune deletes resolved records; dangling widens it to gap records
// naming requirements no longer in the corpus.
func Prune(dangling bool) string {
	if dangling {
		return compose(Tool, VerbPrune, flag(FlagDangling))
	}
	return compose(Tool, VerbPrune)
}

// Retarget is the symbol rename repair.
func Retarget() string { return compose(Tool, VerbRetarget) }

// Init scaffolds a repository; Compile lints the corpus; Verify judges
// the records; PolicyInit derives the accepted test policy — the
// instructions a refusal names its next step with.
func Init() string       { return compose(Tool, VerbInit) }
func Compile() string    { return compose(Tool, VerbCompile) }
func Verify() string     { return compose(Tool, VerbVerify) }
func PolicyInit() string { return compose(Tool, VerbPolicy, VerbInit) }

// composers is every composer by its function name, each rendered
// with placeholder operands in every shape it composes — the parse
// corpus's source, and the one list a new composer must join: a test
// parses this package's source and refuses an exported function
// missing here (every exported function of this package is a
// composer; helpers stay unexported).
var composers = map[string][]func() string{
	"Pin":               {func() string { return Pin("REQ-x") }, func() string { return Pin() }},
	"Unbind":            {func() string { return Unbind("REQ-x", "example.com/p.S", "alpha") }, func() string { return Unbind("REQ-x", "", "") }},
	"Bind":              {func() string { return Bind("REQ-x", "tests", "example.com/p.T") }},
	"Retire":            {func() string { return Retire("REQ-x") }},
	"Supersede":         {func() string { return Supersede([]string{"REQ-a"}, []string{"REQ-b", "REQ-c"}, false) }, func() string { return Supersede([]string{"REQ-a"}, []string{"REQ-b"}, true) }},
	"AttestRequirement": {func() string { return AttestRequirement("REQ-x") }},
	"AttestRetract":     {func() string { return AttestRetract("REQ-x") }},
	"GapRetract":        {func() string { return GapRetract("REQ-x") }},
	"Prune":             {func() string { return Prune(false) }, func() string { return Prune(true) }},
	"Retarget":          {Retarget},
	"Init":              {Init},
	"Compile":           {Compile},
	"Verify":            {Verify},
	"PolicyInit":        {PolicyInit},
}

// Registered lists the composers' names, sorted.
func Registered() []string {
	names := make([]string, 0, len(composers))
	for name := range composers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Samples renders every composer with placeholder operands, in name
// order: the parse test's corpus.
func Samples() []string {
	var out []string
	for _, name := range Registered() {
		for _, render := range composers[name] {
			out = append(out, render())
		}
	}
	return out
}
