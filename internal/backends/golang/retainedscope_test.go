package golang

import (
	"testing"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
)

func TestRetainedScopePreservesEqualBinaryNames(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and executes Go fixture packages")
	}
	neutralAmbient(t)
	n := retainedFixtureInvocation(t, map[string]string{
		"a/same/p_test.go": "package same\nimport \"testing\"\nfunc TestA(t *testing.T){}\n",
		"b/same/p_test.go": "package same\nimport \"testing\"\nfunc TestB(t *testing.T){}\n",
	})
	pkgs := []string{"retained.test/a/same", "retained.test/b/same"}
	s := buildRetainedScope(t.Context(), n, pkgs, retainedProcessHooks{})
	defer s.Close()
	if s.Err != nil {
		t.Fatalf("scope compile=%v stream=%+v", s.Err, s.Stream)
	}
	if s.Packages[pkgs[0]].Artifact.Path == s.Packages[pkgs[1]].Artifact.Path {
		t.Fatal("equal basenames aliased artifacts")
	}
	for _, pkg := range pkgs {
		a := s.Packages[pkg].Artifact
		r := retainedRuntimeFor(t, n, a, pkg, nil, nil)
		result := runRetained(t.Context(), t.Context(), n, r, &stipulatorv1.ProducerIdentity{}, retainedProcessHooks{})
		if !result.HarnessComplete() {
			t.Fatalf("%s failed: %+v", pkg, result)
		}
	}
}

func TestRetainedScopeBuildFailureKeepsSuccessfulSiblingExecution(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and executes Go fixture packages")
	}
	neutralAmbient(t)
	n := retainedFixtureInvocation(t, map[string]string{
		"good/p_test.go": "package good\nimport \"testing\"\nfunc TestGood(t *testing.T){}\n",
		"bad/p_test.go":  "package bad\nimport (\"fmt\";\"testing\")\nfunc TestBad(t *testing.T){fmt.Printf(\"%d\",\"wrong\")}\n",
		"testless/p.go":  "package testless\nfunc Answer() int{return 42}\n",
	})
	s := buildRetainedScope(t.Context(), n, []string{"retained.test/good", "retained.test/bad", "retained.test/testless"}, retainedProcessHooks{})
	defer s.Close()
	if s.Err == nil {
		t.Fatal("vet failure lost")
	}
	bad := s.Packages["retained.test/bad"]
	if bad.Err == nil || bad.Artifact != nil {
		t.Fatalf("failed package has executable admission: %+v", bad)
	}
	good := s.Packages["retained.test/good"]
	if good.Err != nil || good.Artifact == nil || good.Artifact.BindingError == nil {
		t.Fatalf("successful sibling missing ordinary artifact or compilation-binding refusal: %+v; scope=%v", good, s.Err)
	}
	if !s.Packages["retained.test/testless"].NoTestFiles {
		t.Fatal("successful testless sibling swallowed")
	}
	r := retainedRuntimeFor(t, n, good.Artifact, "retained.test/good", nil, nil)
	result := runRetained(t.Context(), t.Context(), n, r, &stipulatorv1.ProducerIdentity{}, retainedProcessHooks{})
	if !result.HarnessComplete() {
		t.Fatalf("ordinary sibling execution=%+v", result)
	}
}
