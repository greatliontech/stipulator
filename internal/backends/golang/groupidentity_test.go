package golang

import (
	"testing"

	"github.com/greatliontech/stipulator/stipulate"
)

// The record-identity coordinate is the policy-declared build
// selection, never the merged ambient environment: a new shell must not
// orphan the store (fingerprints own ambient equivalence, refusing with
// a named reason), while any declared delta — tags, race, environment
// overrides, module mode — is a distinct coordinate
// (REQ-evidence-witness-cache-format).
func TestGoGroupIdentityIgnoresAmbientEnvironment(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-cache-format")
	base := func() *NormalizedInvocation {
		return &NormalizedInvocation{
			Tags: []string{"a"},
			Race: true,
			Env:  []string{"HOME=/one", "PATH=/usr/bin", "TERM=xterm"},
		}
	}
	other := base()
	other.Env = []string{"HOME=/two", "PATH=/opt/bin", "SSH_AUTH_SOCK=/tmp/agent"}
	// Effective (ambient-resolved) build facts must not move the
	// coordinate either: only declared pins partition.
	other.GOFLAGS = "-trimpath"
	other.GOEXPERIMENT = "arenas"
	other.CgoEnabled = true
	if groupIdentity(base()) != groupIdentity(other) {
		t.Fatal("ambient environment or effective build facts moved the record-identity coordinate: a new shell would orphan the store")
	}
	for name, mutate := range map[string]func(*NormalizedInvocation){
		"tags":          func(n *NormalizedInvocation) { n.Tags = []string{"b"} },
		"race":          func(n *NormalizedInvocation) { n.Race = false },
		"env overrides": func(n *NormalizedInvocation) { n.EnvOverrides = []string{"MODE=fast"} },
		"env deny":      func(n *NormalizedInvocation) { n.EnvDeny = []string{"MODE"} },
		"goflags":       func(n *NormalizedInvocation) { n.DeclaredGOFLAGS = declaredPin(true, "-trimpath") },
		"goarch":        func(n *NormalizedInvocation) { n.DeclaredGOARCH = declaredPin(true, "arm64") },
		"cgo":           func(n *NormalizedInvocation) { n.DeclaredCgo = declaredPin(true, "false") },
		"args":          func(n *NormalizedInvocation) { n.Args = []string{"-myflag"} },
		"pgo":           func(n *NormalizedInvocation) { n.PGO = "default.pgo" },
	} {
		changed := base()
		mutate(changed)
		if groupIdentity(base()) == groupIdentity(changed) {
			t.Errorf("declared %s delta did not move the coordinate", name)
		}
	}
	// The ambient-derived delivered width must NOT move the coordinate
	// (a host cgroup change would silently orphan the store; the
	// runtime-config fingerprint owns width equivalence) — only the
	// DECLARED concurrency bound partitions.
	widthed := base()
	widthed.WitnessEnv = []string{"HOME=/one", "GOMAXPROCS=4"}
	if groupIdentity(base()) != groupIdentity(widthed) {
		t.Error("ambient-delivered width moved the coordinate")
	}
	bounded := base()
	bounded.WitnessConcurrency = 3
	if groupIdentity(base()) == groupIdentity(bounded) {
		t.Error("declared concurrency bound did not move the coordinate")
	}
	pinned := base()
	pinned.DeclaredToolchain = "go1.26.5"
	if groupIdentity(base()) == groupIdentity(pinned) {
		t.Error("declared toolchain pin did not move the coordinate")
	}
	// A pure reorder of declared env deltas is presentation, never a
	// new coordinate.
	reordered := base()
	reordered.EnvOverrides = []string{"B=2", "A=1"}
	canonical := base()
	canonical.EnvOverrides = []string{"A=1", "B=2"}
	if groupIdentity(canonical) != groupIdentity(reordered) {
		t.Error("declared env delta reorder moved the coordinate")
	}
	// A joiner-byte value must not alias two entries into one
	// coordinate.
	aliased := base()
	aliased.Args = []string{"-a\x01-b"}
	split := base()
	split.Args = []string{"-a", "-b"}
	if groupIdentity(aliased) == groupIdentity(split) {
		t.Error("joiner byte aliased two argument entries")
	}
}

// A within-group double selection (two same-environment invocations
// naming one package) has no single producing leg: its subjects never
// enter the group's publishable set (REQ-evidence-witness-freshness).
func TestGoGroupSubjectsExcludeAmbiguousPackages(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness")
	g := &captureGroup{
		tests: map[string][]string{
			"example.com/m/clean":  {"TestClean"},
			"example.com/m/shared": {"TestShared"},
		},
		ambiguous: map[string]bool{"example.com/m/shared": true},
	}
	subjects := groupSubjects(g)
	if len(subjects) != 1 || subjects[0].Package != "example.com/m/clean" {
		t.Fatalf("subjects = %v, want only the singly-selected package (ambiguous excluded)", subjects)
	}
}
