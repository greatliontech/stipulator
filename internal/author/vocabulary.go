package author

import (
	"fmt"
	"strings"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/records"
)

// The vocabulary the authoring verbs parse and render: backends, roles,
// excuse classes, landing conditions.

// knownBackends closes the backend-name set: a typo must never author an
// unvalidated binding, on any surface.
var knownBackends = map[string]bool{"go": true, "proto": true}

// roles maps CLI role names to the enum.
var roles = map[string]stipulatorv1.BindingRole{
	"implements": stipulatorv1.BindingRole_BINDING_ROLE_IMPLEMENTS,
	"tests":      stipulatorv1.BindingRole_BINDING_ROLE_TESTS,
	"proves":     stipulatorv1.BindingRole_BINDING_ROLE_PROVES,
}

// ParseRole maps a role flag to the enum. Empty means unspecified (a
// wildcard for unbind, rejected by Bind); an unknown name is an error, so a
// typo can never silently widen an operation.
func ParseRole(s string) (stipulatorv1.BindingRole, error) {
	if s == "" {
		return stipulatorv1.BindingRole_BINDING_ROLE_UNSPECIFIED, nil
	}
	r, ok := roles[s]
	if !ok {
		return 0, fmt.Errorf("unknown role %q (implements, tests, or proves)", s)
	}
	return r, nil
}

// NewExcuses parses declared excuse classes — uncovered, stale, broken
// — validating each name (REQ-gap-verb). Empty input declares nothing:
// the record's default, uncovered alone, applies (REQ-gap-record).
func NewExcuses(names []string) ([]stipulatorv1.GapExcuse, error) {
	var out []stipulatorv1.GapExcuse
	for _, n := range names {
		switch n {
		case "uncovered":
			out = append(out, stipulatorv1.GapExcuse_GAP_EXCUSE_UNCOVERED)
		case "stale":
			out = append(out, stipulatorv1.GapExcuse_GAP_EXCUSE_STALE)
		case "broken":
			out = append(out, stipulatorv1.GapExcuse_GAP_EXCUSE_BROKEN)
		default:
			return nil, fmt.Errorf("unknown excuse class %q (uncovered, stale, broken)", n)
		}
	}
	return out, nil
}

// excuseString renders one excuse class for messages.
func excuseString(x stipulatorv1.GapExcuse) string {
	switch x {
	case stipulatorv1.GapExcuse_GAP_EXCUSE_UNCOVERED:
		return "uncovered"
	case stipulatorv1.GapExcuse_GAP_EXCUSE_STALE:
		return "stale"
	case stipulatorv1.GapExcuse_GAP_EXCUSE_BROKEN:
		return "broken"
	}
	return x.String()
}

// excusesString renders a declared excuse set, naming the default when
// nothing is declared.
func excusesString(xs []stipulatorv1.GapExcuse) string {
	if len(xs) == 0 {
		return "uncovered (default)"
	}
	names := make([]string, 0, len(xs))
	for _, x := range xs {
		names = append(names, excuseString(x))
	}
	return strings.Join(names, ", ")
}

// NewLandingCondition builds a landing condition from mutually exclusive
// flag values; more than one set is an error. Fired marks a manual
// condition already discharged at declaration time — it is meaningless
// on the machine-evaluable conditions.
func NewLandingCondition(covered, exists, manual string, fired, contradicted bool) (*stipulatorv1.LandingCondition, error) {
	set := 0
	for _, v := range []string{covered, exists, manual} {
		if v != "" {
			set++
		}
	}
	if set > 1 {
		return nil, fmt.Errorf("conflicting landing conditions: give exactly one of covered, exists, manual")
	}
	if fired && manual == "" {
		return nil, fmt.Errorf("fired accompanies a manual condition (fire an existing gap with the fired flag alone)")
	}
	// A contradicted letter has no coverage-defined terminal, so the
	// class rides only a manual condition (REQ-gap-conditions).
	if contradicted && manual == "" {
		return nil, fmt.Errorf("contradicted accompanies a manual condition: a contradicted letter has no coverage-defined terminal, name the condition that lands it")
	}
	lc := &stipulatorv1.LandingCondition{}
	switch {
	case covered != "":
		lc.SetCovered(covered)
	case exists != "":
		lc.SetExists(exists)
	case manual != "":
		a := &stipulatorv1.ManualCondition{}
		a.SetCondition(manual)
		// Set only when true: an explicit false would give the field
		// presence, making proto.Equal see a retarget against every prior
		// record that simply lacks it.
		if fired {
			a.SetFired(true)
		}
		if contradicted {
			a.SetContradicted(true)
		}
		lc.SetManual(a)
	default:
		return nil, nil
	}
	return lc, nil
}

// landingConditionString renders a landing condition human-readably, for
// surfacing retargets.
func landingConditionString(lc *stipulatorv1.LandingCondition) string {
	switch {
	case lc == nil:
		return "none"
	case lc.HasCovered():
		return "covered(" + lc.GetCovered() + ")"
	case lc.HasExists():
		return "exists(" + lc.GetExists() + ")"
	case lc.HasManual():
		s := "manual(" + lc.GetManual().GetCondition() + ")"
		for _, flag := range records.ManualFlags(lc.GetManual().GetContradicted(), lc.GetManual().GetFired()) {
			s += " [" + flag + "]"
		}
		return s
	}
	return "none"
}

// SelfSentinel is the bulk landing sentinel: covered(self) resolves to
// each named requirement's own coverage (REQ-gap-bulk). It can never
// collide with a real identifier — the profile's ID pattern requires the
// REQ- prefix.
const SelfSentinel = "self"
