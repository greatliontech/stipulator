package golang

import (
	"slices"
	"testing"

	"pgregory.net/rapid"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/stipulate"
)

// A reviewed runtime-only execution bound edits without re-addressing
// the store: the capture group and the record-identity coordinate both
// key on the identity-bearing argument partition alone, so a budget
// knob is tunable without discarding every stored record. The
// classification fails closed — an argument the table cannot prove
// runtime-only stays identity-bearing, unrecognized spellings included
// (REQ-evidence-witness-cache-format, REQ-evidence-witness-freshness).
func TestRuntimeOnlyArgsDoNotReaddressRecords(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-cache-format")
	stipulate.Covers(t, "REQ-evidence-witness-freshness")
	base := func() *NormalizedInvocation {
		return &NormalizedInvocation{
			Tags: []string{"a"},
			Race: true,
			Args: []string{"-test.timeout=30m"},
		}
	}
	edited := base()
	edited.Args = []string{"-test.timeout=100m"}
	if groupIdentity(base()) != groupIdentity(edited) {
		t.Error("a -test.timeout edit re-addressed the record coordinate: no execution budget is tunable without a cold rebuild")
	}
	if groupKey(base()) != groupKey(edited) {
		t.Error("a -test.timeout edit split the capture group")
	}
	removed := base()
	removed.Args = nil
	if groupIdentity(base()) != groupIdentity(removed) {
		t.Error("removing the runtime-only bound re-addressed the record coordinate")
	}
	// Fail closed: an argument the table cannot prove runtime-only is
	// identity-bearing — a custom flag steers test behavior outside
	// the guarded testing runtime-configuration API.
	unknown := base()
	unknown.Args = []string{"-test.timeout=30m", "-fixture=big"}
	unknownEdited := base()
	unknownEdited.Args = []string{"-test.timeout=30m", "-fixture=small"}
	if groupIdentity(unknown) == groupIdentity(unknownEdited) {
		t.Error("an unrecognized argument edit did not move the coordinate: the classification must fail closed")
	}
	// Fail closed on spelling: only the reviewed single-token "=" form
	// bypasses identity. The bare flag is not that spelling — its
	// presence must move the coordinate.
	bare := base()
	bare.Args = []string{"-test.timeout"}
	none := base()
	none.Args = nil
	if groupIdentity(bare) == groupIdentity(none) {
		t.Error("the bare -test.timeout flag was treated as runtime-only: only the reviewed single-token = form may bypass identity")
	}
	// And the two-token form's tokens both stay identity-bearing.
	twoToken := base()
	twoToken.Args = []string{"-test.timeout", "30m"}
	twoTokenEdited := base()
	twoTokenEdited.Args = []string{"-test.timeout", "100m"}
	if groupIdentity(twoToken) == groupIdentity(twoTokenEdited) {
		t.Error("the two-token timeout spelling was treated as runtime-only: only the reviewed single-token form may bypass identity")
	}
}

// keyFieldTuple is the canonical field tuple a key function must be
// injective over; the property below pins collision-freedom against it.
type keyFieldTuple struct {
	strs  [][]string
	flags []bool
	one   []string
	ints  []int32
}

func tupleEqual(a, b keyFieldTuple) bool {
	if !slices.Equal(a.flags, b.flags) || !slices.Equal(a.one, b.one) || !slices.Equal(a.ints, b.ints) || len(a.strs) != len(b.strs) {
		return false
	}
	for i := range a.strs {
		if !slices.Equal(a.strs[i], b.strs[i]) {
			return false
		}
	}
	return true
}

func groupKeyFields(n *NormalizedInvocation) keyFieldTuple {
	return keyFieldTuple{
		strs:  [][]string{n.Tags, witnessEnvOf(n), identityArgs(n.Args), canonicalExclusions(n.ExcludedPaths), n.Vouches},
		flags: []bool{n.AssumePure, n.Race},
		one:   []string{n.ModuleMode.String(), n.PGO},
	}
}

func groupIdentityFields(n *NormalizedInvocation) keyFieldTuple {
	return keyFieldTuple{
		strs:  [][]string{n.Tags, identityArgs(n.Args), sortedCopy(n.EnvOverrides), sortedCopy(n.EnvDeny)},
		flags: []bool{n.Race, n.WorkspaceOn},
		one: []string{n.DeclaredGOOS, n.DeclaredGOARCH, n.DeclaredCgo, n.DeclaredGOFLAGS,
			n.ModuleMode.String(), n.PGO, n.DeclaredToolchain},
		ints: []int32{n.WitnessConcurrency},
	}
}

// TestKeyEncodingIsCollisionFree: key equality holds exactly when the
// canonical field tuple is equal, for adversarial values including
// separator bytes, quotes, and label-shaped fragments
// (REQ-evidence-witness-cache-format, REQ-evidence-witness-freshness).
// The anchored pairs pin the two known aliasing channels; the property
// then perturbs ONE field of a clone per case — collision detection
// needs near-identical pairs, which independent draws never produce.
func TestKeyEncodingIsCollisionFree(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-cache-format")
	stipulate.Covers(t, "REQ-evidence-witness-freshness")
	// Anchored alias, env values: a policy-declared environment VALUE
	// may legally carry \x01 (validation refuses only NUL), so the
	// unquoted \x01 join aliased two declared environments into one
	// capture group — the group's engine took the first invocation's
	// env while the second's records published under it.
	envAliased := &NormalizedInvocation{WitnessEnv: []string{"K=V", "A=x\x01B=y"}}
	envSplit := &NormalizedInvocation{WitnessEnv: []string{"K=V", "A=x", "B=y"}}
	if groupKey(envAliased) == groupKey(envSplit) {
		t.Error("a \\x01-carrying env value aliased two witness environments into one capture group")
	}
	// Anchored alias, cooperating raw scalars: adjacent unquoted
	// segments (cgo, GOFLAGS) let each value smuggle the other's label
	// so the concatenations align byte for byte. Structurally
	// unreachable through NormalizeInvocation's validation — quoting
	// every segment is the uniform rule that keeps it that way without
	// the encoding depending on validation staying in sync.
	aliasA := &NormalizedInvocation{DeclaredCgo: "X\x00goflags=Y", DeclaredGOFLAGS: "Z"}
	aliasB := &NormalizedInvocation{DeclaredCgo: "X", DeclaredGOFLAGS: "Y\x00goflags=Z"}
	if groupIdentity(aliasA) == groupIdentity(aliasB) {
		t.Error("cooperating raw scalars aliased two configurations into one record coordinate")
	}
	gen := rapid.Custom(func(t *rapid.T) *NormalizedInvocation {
		return &NormalizedInvocation{
			Tags:               adversarialList.Draw(t, "tags"),
			WitnessEnv:         append([]string{"K=V"}, adversarialList.Draw(t, "env")...),
			Args:               adversarialList.Draw(t, "args"),
			ExcludedPaths:      adversarialList.Draw(t, "excl"),
			Vouches:            adversarialList.Draw(t, "vouches"),
			EnvOverrides:       adversarialList.Draw(t, "envset"),
			EnvDeny:            adversarialList.Draw(t, "envdeny"),
			AssumePure:         rapid.Bool().Draw(t, "pure"),
			Race:               rapid.Bool().Draw(t, "race"),
			WorkspaceOn:        rapid.Bool().Draw(t, "workspace"),
			ModuleMode:         rapid.SampledFrom([]stipulatorv1.GoModuleMode{stipulatorv1.GoModuleMode_GO_MODULE_MODE_UNSPECIFIED, stipulatorv1.GoModuleMode_GO_MODULE_MODE_VENDOR}).Draw(t, "mode"),
			PGO:                adversarialVal.Draw(t, "pgo"),
			DeclaredGOOS:       adversarialVal.Draw(t, "goos"),
			DeclaredGOARCH:     adversarialVal.Draw(t, "goarch"),
			DeclaredCgo:        adversarialVal.Draw(t, "cgo"),
			DeclaredGOFLAGS:    adversarialVal.Draw(t, "goflags"),
			DeclaredToolchain:  adversarialVal.Draw(t, "toolchain"),
			WitnessConcurrency: rapid.Int32Range(0, 2).Draw(t, "concurrency"),
		}
	})
	// The perturbation draws a segment LABEL and replaces that field
	// on a clone (or leaves it identical): the pair is near-identical
	// by construction, so an encoding that folds any single field's
	// distinction — or leaks one field's bytes into a neighbor — is a
	// drawn case away, not a coincidence of independent draws. The
	// domain is the keys' own label set (keyedLabels), so a field
	// keyed tomorrow without a perturbation entry fails
	// TestPerturbationDomainCoversEveryKeyedField instead of going
	// silently blind on both sides of the biconditional.
	labels := keyedLabels()
	rapid.Check(t, func(rt *rapid.T) {
		a := gen.Draw(rt, "a")
		c := *a
		if rapid.Bool().Draw(rt, "perturbed") {
			perturbations[rapid.SampledFrom(labels).Draw(rt, "field")](rt, &c)
		}
		b := &c
		if (groupKey(a) == groupKey(b)) != tupleEqual(groupKeyFields(a), groupKeyFields(b)) {
			rt.Fatalf("groupKey collision or spurious split:\n a=%+v\n b=%+v", a, b)
		}
		if (groupIdentity(a) == groupIdentity(b)) != tupleEqual(groupIdentityFields(a), groupIdentityFields(b)) {
			rt.Fatalf("groupIdentity collision or spurious split:\n a=%+v\n b=%+v", a, b)
		}
	})
}

// adversarialVal and adversarialList are the shared draw pools for the
// collision property and the perturbation registry: separator bytes,
// quotes, label-shaped fragments, and the runtime-only spellings.
var adversarialVal = rapid.SampledFrom([]string{
	"", "a", "b", "a,b", `"`, `\"`,
	"a\x00b", "a\x01b", "x\x00args=", "\x00race=true", "A=1", "A=1\x01B=2", "A=x\x01B=y",
	"-test.timeout=30m", "-test.timeout=100m", "-test.timeout", "30m", "-fixture=big",
})

var adversarialList = rapid.SliceOfN(adversarialVal, 0, 3)

// perturbations maps every keyed segment label to a draw replacing its
// underlying invocation field; TestPerturbationDomainCoversEveryKeyedField
// holds this map and the keys' segment tables in exact correspondence,
// label sets AND label-to-field wiring.
var perturbations = map[string]func(t *rapid.T, n *NormalizedInvocation){
	"tags":       func(t *rapid.T, n *NormalizedInvocation) { n.Tags = adversarialList.Draw(t, "tags2") },
	"env":        func(t *rapid.T, n *NormalizedInvocation) { n.WitnessEnv = append([]string{"K=V"}, adversarialList.Draw(t, "env2")...) },
	"args":       func(t *rapid.T, n *NormalizedInvocation) { n.Args = adversarialList.Draw(t, "args2") },
	"exclusions": func(t *rapid.T, n *NormalizedInvocation) { n.ExcludedPaths = adversarialList.Draw(t, "excl2") },
	"vouches":    func(t *rapid.T, n *NormalizedInvocation) { n.Vouches = adversarialList.Draw(t, "vouches2") },
	"envset":     func(t *rapid.T, n *NormalizedInvocation) { n.EnvOverrides = adversarialList.Draw(t, "envset2") },
	"envdeny":    func(t *rapid.T, n *NormalizedInvocation) { n.EnvDeny = adversarialList.Draw(t, "envdeny2") },
	"pure":       func(t *rapid.T, n *NormalizedInvocation) { n.AssumePure = !n.AssumePure },
	"race":       func(t *rapid.T, n *NormalizedInvocation) { n.Race = !n.Race },
	"workspace":  func(t *rapid.T, n *NormalizedInvocation) { n.WorkspaceOn = !n.WorkspaceOn },
	"modulemode": func(t *rapid.T, n *NormalizedInvocation) {
		n.ModuleMode = rapid.SampledFrom([]stipulatorv1.GoModuleMode{stipulatorv1.GoModuleMode_GO_MODULE_MODE_UNSPECIFIED, stipulatorv1.GoModuleMode_GO_MODULE_MODE_VENDOR}).Draw(t, "mode2")
	},
	"pgo":         func(t *rapid.T, n *NormalizedInvocation) { n.PGO = adversarialVal.Draw(t, "pgo2") },
	"goos":        func(t *rapid.T, n *NormalizedInvocation) { n.DeclaredGOOS = adversarialVal.Draw(t, "goos2") },
	"goarch":      func(t *rapid.T, n *NormalizedInvocation) { n.DeclaredGOARCH = adversarialVal.Draw(t, "goarch2") },
	"cgo":         func(t *rapid.T, n *NormalizedInvocation) { n.DeclaredCgo = adversarialVal.Draw(t, "cgo2") },
	"goflags":     func(t *rapid.T, n *NormalizedInvocation) { n.DeclaredGOFLAGS = adversarialVal.Draw(t, "goflags2") },
	"toolchain":   func(t *rapid.T, n *NormalizedInvocation) { n.DeclaredToolchain = adversarialVal.Draw(t, "toolchain2") },
	"concurrency": func(t *rapid.T, n *NormalizedInvocation) { n.WitnessConcurrency = rapid.Int32Range(0, 2).Draw(t, "concurrency2") },
}

// keyedLabels is the union of both segment tables' labels, sorted. The
// probe invocation pins WitnessEnv non-nil so label enumeration never
// falls through to the host-width derivation — labels are structural,
// not value-dependent.
func keyedLabels() []string {
	n := &NormalizedInvocation{WitnessEnv: []string{}}
	seen := map[string]bool{}
	var labels []string
	for _, s := range append(groupKeySegments(n), groupIdentitySegments(n)...) {
		if !seen[s.label] {
			seen[s.label] = true
			labels = append(labels, s.label)
		}
	}
	slices.Sort(labels)
	return labels
}

// changedLabels lists the labels whose segment value differs between
// two invocations, across both tables, deduplicated.
func changedLabels(a, b *NormalizedInvocation) []string {
	segsA := append(groupKeySegments(a), groupIdentitySegments(a)...)
	segsB := append(groupKeySegments(b), groupIdentitySegments(b)...)
	seen := map[string]bool{}
	var out []string
	for i := range segsA {
		if segsA[i].value != segsB[i].value && !seen[segsA[i].label] {
			seen[segsA[i].label] = true
			out = append(out, segsA[i].label)
		}
	}
	return out
}

// TestPerturbationDomainCoversEveryKeyedField holds the perturbation
// registry and the keys' own segment tables in exact correspondence:
// (1) label sets match both directions — a segment added to either key
// without a registry entry, or a stale entry for a segment no key
// emits, fails loudly; (2) each entry moves exactly its own label's
// segment and provably moves it at least once — a mis-wired entry
// (label L driving field M) would re-open the silent-blindness channel
// one level down (REQ-evidence-witness-cache-format).
func TestPerturbationDomainCoversEveryKeyedField(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-cache-format")
	labels := keyedLabels()
	for _, l := range labels {
		if perturbations[l] == nil {
			t.Errorf("keyed segment %q has no perturbation entry: the collision property is blind to it", l)
		}
	}
	keyed := map[string]bool{}
	for _, l := range labels {
		keyed[l] = true
	}
	for l := range perturbations {
		if !keyed[l] {
			t.Errorf("perturbation entry %q matches no keyed segment", l)
		}
	}
	if t.Failed() {
		return
	}
	for _, label := range labels {
		t.Run(label, func(t *testing.T) {
			moved := false
			rapid.Check(t, func(rt *rapid.T) {
				a := &NormalizedInvocation{WitnessEnv: []string{"K=V"}}
				c := *a
				perturbations[label](rt, &c)
				for _, changed := range changedLabels(a, &c) {
					if changed != label {
						rt.Fatalf("perturbation %q moved segment %q", label, changed)
					}
				}
				if len(changedLabels(a, &c)) > 0 {
					moved = true
				}
			})
			if !moved {
				t.Errorf("perturbation %q never moved its own segment across the run", label)
			}
		})
	}
}
