package compile

import (
	"strings"
	"testing"
	"unicode"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/proptest"
	"github.com/greatliontech/stipulator/stipulate"
	"google.golang.org/protobuf/proto"
	"pgregory.net/rapid"
)

// stripLocations clears every location-metadata field, leaving exactly
// the content the layout-independence invariant quantifies over.
func stripLocations(s *stipulatorv1.Spec) *stipulatorv1.Spec {
	c := proto.Clone(s).(*stipulatorv1.Spec)
	c.SetDocuments(nil)
	for _, r := range c.GetRequirements() {
		r.ClearLocation()
	}
	for _, tm := range c.GetTerms() {
		tm.ClearLocation()
	}
	for _, n := range c.GetNotes() {
		n.ClearLocation()
	}
	for _, a := range c.GetAnnotations() {
		a.ClearLocation()
	}
	return c
}

func compileClean(rt *rapid.T, files map[string]string) *stipulatorv1.Spec {
	spec, diags, err := Compile(proptest.FS(files, nil))
	if err != nil {
		rt.Fatalf("compile: %v", err)
	}
	if len(diags) > 0 {
		rt.Fatalf("generated corpus not clean: %v\n%v", diags, files)
	}
	return spec
}

// TestPropLayoutIndependence quantifies the layout invariants: any two
// partitions of one block pool compile to IRs identical modulo location
// metadata, and location never contributes to identities or content
// hashes.
func TestPropLayoutIndependence(t *testing.T) {
	stipulate.Covers(t, "REQ-model-layout-independence", "REQ-model-location-metadata")
	rapid.Check(t, func(rt *rapid.T) {
		c := proptest.Gen(rt)
		one := compileClean(rt, c.Partition(rt, "one"))
		two := compileClean(rt, c.Partition(rt, "two"))

		// Location metadata contributes nothing to identity or hashes.
		hashes := func(s *stipulatorv1.Spec) map[string]string {
			m := map[string]string{}
			for _, r := range s.GetRequirements() {
				m[r.GetId()] = r.GetContentHash()
			}
			for _, tm := range s.GetTerms() {
				m["term:"+tm.GetName()] = tm.GetContentHash()
			}
			return m
		}
		hOne, hTwo := hashes(one), hashes(two)
		if len(hOne) != len(hTwo) {
			rt.Fatalf("identity sets differ: %v vs %v", hOne, hTwo)
		}
		for id, h := range hOne {
			if hTwo[id] != h {
				rt.Fatalf("content hash of %s moved across layouts: %s vs %s", id, h, hTwo[id])
			}
		}

		// The full IR is identical modulo location.
		a, b := stripLocations(one), stripLocations(two)
		if !proto.Equal(a, b) {
			rt.Fatalf("IRs differ beyond location metadata:\n%v\n---\n%v", a, b)
		}
	})
}

// caseVariant flips the case of a random subset of letters; tombstone
// comparison is case-insensitive, so every variant must still collide.
func caseVariant(rt *rapid.T, s string) string {
	runes := []rune(s)
	for i, r := range runes {
		if unicode.IsLetter(r) && rapid.Bool().Draw(rt, "flip") {
			if unicode.IsUpper(r) {
				runes[i] = unicode.ToLower(r)
			} else {
				runes[i] = unicode.ToUpper(r)
			}
		}
	}
	return string(runes)
}

// TestPropTombstonedIdentityNeverRedeclared quantifies identity
// permanence: any declared identity, tombstoned under any letter case,
// makes the corpus refuse to compile cleanly.
func TestPropTombstonedIdentityNeverRedeclared(t *testing.T) {
	stipulate.Covers(t, "REQ-model-identity")
	rapid.Check(t, func(rt *rapid.T) {
		c := proptest.Gen(rt)
		identities := append([]string{}, c.ReqIDs...)
		identities = append(identities, c.TermNames...)
		victim := rapid.SampledFrom(identities).Draw(rt, "victim")

		files := c.Partition(rt, "p")
		fsys := proptest.FS(files, map[string]string{
			".stipulator/tombstones.textproto": "retired: \"" + caseVariant(rt, victim) + "\"\n",
		})
		_, diags, err := Compile(fsys)
		if err != nil {
			rt.Fatalf("compile: %v", err)
		}
		for _, d := range diags {
			if strings.Contains(d.Message, "redeclares a tombstoned identity") {
				return
			}
		}
		rt.Fatalf("tombstoned identity %s redeclared without diagnostic: %v", victim, diags)
	})
}
