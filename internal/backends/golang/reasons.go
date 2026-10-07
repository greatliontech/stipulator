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

// reasonClass is one class of uncacheable reason the backend mints:
// the prefix every reason of the class begins with and the explain
// kind that derives it. Every composer of an uncacheable reason reads
// its class here, so the prefix has one spelling and no reason
// reaches the uncacheable face without an explain kind
// (REQ-evidence-witness-freshness's diagnosable set; REQ-mcp-explain).
type reasonClass struct {
	prefix string
	kind   explainKind
}

// with composes a reason of the class over its detail.
func (c reasonClass) with(detail string) string { return c.prefix + detail }

// The reason classes. The seeding family's two spellings share one
// prefix; the judgment vocabulary's reasons are whole and carry no
// detail.
var (
	reasonPostRun          = reasonClass{"post-run validation: ", explainCulprit}
	reasonObservationSeal  = reasonClass{"observation sealed: ", explainCulprit}
	reasonProofRefused     = reasonClass{"observation proof refused: ", explainCulprit}
	reasonSeeded           = reasonClass{"random-seeded property witness", explainWitness}
	reasonSeedingRefused   = reasonClass{"unclassifiable seeding: " + neverServedSuffix + ": ", explainWitness}
	reasonUnclassifiable   = reasonClass{"unclassifiable witness: " + neverServedSuffix + ": ", explainSelf}
	reasonProducerFault    = reasonClass{"post-run producer validation faulted: ", explainSelf}
	reasonStateUnavailable = reasonClass{"observation state unavailable: ", explainSelf}
	reasonSourceFailed     = reasonClass{"source producer validation failed: ", explainSelf}
	reasonDegraded         = reasonClass{"freshness path degraded: ", explainSelf}
	reasonNotPublished     = reasonClass{"record not published", explainSelf}
	reasonNoCapture        = reasonClass{"no capture group: ", explainSelf}
	reasonJudged           = reasonClass{"", explainSelf}
)

// neverServedSuffix is the serving refusal every fail-closed
// classification reason carries: the subject executes every run.
const neverServedSuffix = "executes every run, never served (absence of proof never serves)"

// reasonClasses lists every class with a prefix of its own; the
// judgment vocabulary (judgeSubject's whole reasons) is listed by its
// members below, each its own exact spelling.
var reasonClasses = []reasonClass{
	reasonPostRun, reasonObservationSeal, reasonProofRefused,
	reasonSeeded, reasonSeedingRefused, reasonUnclassifiable,
	reasonProducerFault, reasonStateUnavailable, reasonSourceFailed,
	reasonDegraded, reasonNotPublished, reasonNoCapture,
}

// judgedReasons are the per-subject publish judgment's whole reasons
// (judgment.go), each a reason of the judged class.
var judgedReasons = []string{
	reasonNoProducingLeg, reasonNoFingerprint, reasonNoTerminalEvent,
	reasonFlushUnproven, reasonProducerUnhealthy, reasonNoHealthyOutcome,
}

// classifyReason answers the class of an uncacheable reason: the
// listed prefix the reason begins with — the prefixes are prefix-free
// (no class's prefix begins another's, pinned), so at most one matches
// — or the judged class for a judgment reason spelled whole. A reason
// no class owns is a foreign spelling — explain treats it as a
// freshness-library tail passed bare, then refuses.
func classifyReason(reason string) (reasonClass, bool) {
	for _, c := range reasonClasses {
		if strings.HasPrefix(reason, c.prefix) {
			return c, true
		}
	}
	for _, r := range judgedReasons {
		if reason == r {
			return reasonJudged, true
		}
	}
	return reasonClass{}, false
}
