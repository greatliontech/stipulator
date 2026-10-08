package golang

import "strings"

// explainKind is what the explain verb derives for an uncacheable
// reason of a class: the dynamic-state chain parsed from a culprit the
// reason's tail names (an attribution carrying no culprit is its own
// derivation), the witness's own seeding derivation (the reason names
// no witness, so the verb takes the witness), or nothing beyond the
// reason — it is its own attribution (REQ-mcp-explain).
type explainKind int

const (
	// explainCulprit: a freshness-library verdict reason rides the
	// class's prefix; a dynamic-state culprit parsed from its tail
	// yields the chain, any other attribution answers as itself.
	explainCulprit explainKind = iota + 1
	// explainWitness: the seeding family — the derivation is the bound
	// body's walk to a run-time-seeded driver, answered for a witness.
	explainWitness
	// explainSelf: the reason is its own attribution.
	explainSelf
)

// reasonClass names one class of uncacheable reason the backend mints:
// an index into reasonTable, the one place a class's prefix and
// explain kind are spelled. A reason is minted over a class alone
// (with), so every reason the backend carries renders a listed prefix
// and explains by a listed kind — a class the table does not list has
// no prefix to render and no kind to explain by (an index past the
// table faults at its first render), and a prefix spelled anywhere
// else can enter no reason (REQ-evidence-witness-freshness's
// diagnosable set; REQ-mcp-explain).
type reasonClass uint8

// The reason classes, in the table's order. The seeding family's two
// spellings share one prefix; the judgment vocabulary's reasons are
// whole and carry no detail (reasonJudged's prefix is empty).
// reasonNone is the zero class — no reason: the type's zero value and
// classifyReason's miss — with no prefix and no explain kind, so a
// reason never minted renders empty and explains by nothing rather
// than as a listed class with an empty detail.
const (
	reasonNone reasonClass = iota
	reasonPostRun
	reasonObservationSeal
	reasonProofRefused
	reasonSeeded
	reasonSeedingRefused
	reasonUnclassifiable
	reasonProducerFault
	reasonStateUnavailable
	reasonSourceFailed
	reasonDegraded
	reasonNotPublished
	reasonNoCapture
	reasonStoreRefused
	reasonJudged
)

// reasonTable spells every class: the prefix every reason of the class
// begins with and the explain kind that derives it.
var reasonTable = [...]struct {
	prefix string
	kind   explainKind
}{
	reasonNone:             {"", 0},
	reasonPostRun:          {"post-run validation: ", explainCulprit},
	reasonObservationSeal:  {"observation sealed: ", explainCulprit},
	reasonProofRefused:     {"observation proof refused: ", explainCulprit},
	reasonSeeded:           {"random-seeded property witness", explainWitness},
	reasonSeedingRefused:   {"unclassifiable seeding: " + neverServedSuffix + ": ", explainWitness},
	reasonUnclassifiable:   {"unclassifiable witness: " + neverServedSuffix + ": ", explainSelf},
	reasonProducerFault:    {"post-run producer validation faulted: ", explainSelf},
	reasonStateUnavailable: {"observation state unavailable: ", explainSelf},
	reasonSourceFailed:     {"source producer validation failed: ", explainSelf},
	reasonDegraded:         {"freshness path degraded: ", explainSelf},
	reasonNotPublished:     {"record not published", explainSelf},
	reasonNoCapture:        {"no capture group: ", explainSelf},
	reasonStoreRefused:     {"the store refused the record: ", explainSelf},
	reasonJudged:           {"", explainSelf},
}

// neverServedSuffix is the serving refusal every fail-closed
// classification reason carries: the subject executes every run.
const neverServedSuffix = "executes every run, never served (absence of proof never serves)"

// foreignSpellingDetail leads the detail of a serving refusal whose
// spelling no class owns, held as an unclassifiable witness.
const foreignSpellingDetail = "a serving refusal of no listed class: "

// prefix is the class's prefix, every reason of the class begins with.
func (c reasonClass) prefix() string { return reasonTable[c].prefix }

// kind is the explain kind that derives a reason of the class.
func (c reasonClass) kind() explainKind { return reasonTable[c].kind }

// uncacheable is one uncacheable reason: its class and the detail the
// class's prefix leads. It carries a class, and so a listed prefix and
// explain kind (with mints one over a class; parseReason admits a
// text under its class), so every reason the backend carries is
// rendered and explained through the table — the maps hold the type,
// and the text form (String) is taken once where the reason leaves
// the backend: the result's reasons, the resolver wire, a record, the
// executed-reason account. The zero value is reasonNone over no
// detail: no reason.
type uncacheable struct {
	class  reasonClass
	detail string
}

// String is the reason's one text form: the class's prefix, the detail.
func (u uncacheable) String() string { return u.class.prefix() + u.detail }

// with composes a reason of the class over its detail.
func (c reasonClass) with(detail string) uncacheable { return uncacheable{c, detail} }

// parseReason admits a reason spelled as text — one that crossed the
// resolver wire or sits in a record — under the class its prefix
// names, the detail the rest. A spelling no class owns is admitted
// fail-closed as an unclassifiable witness whose detail is the foreign
// spelling (foreignSpellingDetail): it never serves, and explain
// answers it as its own attribution — a judged reason carrying a
// detail is such a spelling, since the judged class spells its reasons
// whole; a wrapped spelling parsed again is its own class, unchanged
// (the resolver child is this build, pinned at the handshake, so a
// foreign spelling is a record of an earlier one).
func parseReason(text string) uncacheable {
	if c, ok := classifyReason(text); ok {
		return uncacheable{c, strings.TrimPrefix(text, c.prefix())}
	}
	return reasonUnclassifiable.with(foreignSpellingDetail + text)
}

// reasonClasses lists every class with a prefix of its own — the
// table's classes but the none class and the judged one, whose
// reasons are listed by their members (judgedReasons), each its own
// exact spelling.
var reasonClasses = func() []reasonClass {
	var classes []reasonClass
	for c := range reasonClass(len(reasonTable)) {
		if c != reasonNone && c != reasonJudged {
			classes = append(classes, c)
		}
	}
	return classes
}()

// judgedReasons are the per-subject publish judgment's whole reasons
// (judgment.go), each a reason of the judged class.
var judgedReasons = []uncacheable{
	reasonNoProducingLeg, reasonNoFingerprint, reasonNoTerminalEvent,
	reasonFlushUnproven, reasonProducerUnhealthy, reasonNoHealthyOutcome,
}

// classifyReason answers the class of an uncacheable reason spelled as
// text: the listed prefix the text begins with — the prefixes are
// prefix-free (no class's prefix begins another's, pinned), so at most
// one matches — or the judged class for a judgment reason spelled
// whole. A text no class owns is a foreign spelling, reasonNone and
// false — explain treats it as a freshness-library tail passed bare,
// then refuses; parseReason holds it as an unclassifiable witness.
func classifyReason(reason string) (reasonClass, bool) {
	for _, c := range reasonClasses {
		if strings.HasPrefix(reason, c.prefix()) {
			return c, true
		}
	}
	for _, r := range judgedReasons {
		if reason == r.String() {
			return reasonJudged, true
		}
	}
	return reasonNone, false
}
