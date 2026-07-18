// Package policy handles the accepted test policy record's wire form: its
// committed location, strict parsing, and canonical-form validation.
//
// The package touches only the backend-neutral envelope — canonical
// invocation identity, the explicit timeout, and the presence of exactly
// one typed backend payload. Payload contents are opaque here and are
// interpreted by the named backend at dispatch, which is what keeps the
// core policy model backend-neutral. Canonical form is refused, never
// repaired: the record is reviewed contract, so a loader that reordered or
// defaulted it would run something other than what was reviewed.
package policy

import (
	"fmt"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"google.golang.org/protobuf/encoding/prototext"
)

// Path is the policy record's location, fixed relative to the repository
// root.
const Path = ".stipulator/policy.textproto"

// Parse strict-parses a policy record and validates canonical form. Any
// violation — a syntax error, an unknown or duplicated field, or a
// non-canonical policy — refuses the whole record.
func Parse(raw []byte) (*stipulatorv1.TestPolicy, error) {
	p := &stipulatorv1.TestPolicy{}
	if err := prototext.Unmarshal(raw, p); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", Path, err)
	}
	if err := Validate(p); err != nil {
		return nil, err
	}
	return p, nil
}

// Validate checks canonical form over the backend-neutral envelope:
// invocation names non-empty, unique, and strictly ascending in byte
// order; every invocation carrying a positive explicit timeout and
// exactly one typed backend payload.
func Validate(p *stipulatorv1.TestPolicy) error {
	prev := ""
	for i, inv := range p.GetInvocations() {
		name := inv.GetName()
		if name == "" {
			return fmt.Errorf("invocation %d: empty name", i)
		}
		if i > 0 && name <= prev {
			if name == prev {
				return fmt.Errorf("invocation %q: duplicate name", name)
			}
			return fmt.Errorf("invocation %q: not in canonical order (after %q)", name, prev)
		}
		prev = name
		// The timeout is required and positive: an explicit declaration is
		// the review consent for a long-running invocation, so an absent
		// or non-positive one cannot default.
		to := inv.GetTimeout()
		if to == nil {
			return fmt.Errorf("invocation %q: missing explicit timeout", name)
		}
		// Validity precedes sign: an out-of-range Duration has multiple
		// byte-different renderings and saturating readers, so the same
		// reviewed record could execute differently between hosts.
		if err := to.CheckValid(); err != nil {
			return fmt.Errorf("invocation %q: invalid timeout: %v", name, err)
		}
		if to.AsDuration() <= 0 {
			return fmt.Errorf("invocation %q: timeout must be positive", name)
		}
		if inv.WhichConfig() == stipulatorv1.PolicyInvocation_Config_not_set_case {
			return fmt.Errorf("invocation %q: missing typed backend payload", name)
		}
	}
	return nil
}
