package golang

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/greatliontech/gofresh/gotool"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
)

func TestRetainedCoverageAssignmentsMatchGo(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and executes Go fixture packages")
	}
	neutralAmbient(t)
	n := retainedFixtureInvocation(t, map[string]string{
		"p.go": "package p\nfunc Answer() int{return 42}\n",
		"p_test.go": `package p
import ("encoding/json";"os";"testing")
func TestCoverage(t *testing.T){
 if Answer()!=42{t.Fatal("answer")}
 _,dir:=os.LookupEnv("GOCOVERDIR")
 data,_:=json.Marshal(struct{Mode string; Directory bool}{testing.CoverMode(),dir})
 if err:=os.WriteFile("observed.json",data,0600);err!=nil{t.Fatal(err)}
}
`,
	})
	n.WitnessEnv = gotool.UnsetEnv(witnessEnvOf(n), "GOCOVERDIR")
	for _, flags := range []string{
		"-covermode=atomic -cover=false", "-cover=false -covermode=atomic",
		"-coverpkg=./... -cover=false", "-cover=false -coverpkg=",
		"-covermode=", "-coverprofile=", "-test.coverprofile=",
		"-coverprofile= -cover=false", "-cover=false -test.coverprofile=",
		"-coverprofile=coverage.txt -cover=false",
	} {
		t.Run(flags, func(t *testing.T) {
			nc := *n
			retainedSetFlags(&nc, flags)
			nc.CacheBypass = true
			if err := validateGoflags(flags); err != nil {
				t.Fatalf("case must be admitted: %v", err)
			}
			profile, err := openRetainedCoverage(&nc)
			if err != nil {
				t.Fatal(err)
			}
			a := retainedBuildOK(t, &nc, "retained.test")
			r := retainedRuntimeFor(t, &nc, a, "retained.test", nil, profile)
			got := runRetained(t.Context(), t.Context(), &nc, r, &stipulatorv1.ProducerIdentity{}, retainedProcessHooks{})
			if err := profile.Close(); err != nil {
				t.Fatal(err)
			}
			data, readErr := os.ReadFile(filepath.Join(n.Dir, "observed.json"))
			_ = os.Remove(filepath.Join(n.Dir, "observed.json"))
			out, goErr := ownedRunner.Run(t.Context(), nc.Dir, witnessEnvOf(&nc), testCommandArgs(&nc, "retained.test", nil, "")...)
			if got.BinaryState == nil || got.BinaryState.Success() != (goErr == nil) {
				t.Fatalf("retained status %v (%v) differs from Go %v: %s", got.BinaryState, got.BinaryErr, goErr, out)
			}
			if goErr != nil {
				return
			} // A profile explicitly left on a non-cover binary is a real test error.
			want, err := os.ReadFile(filepath.Join(n.Dir, "observed.json"))
			if err != nil {
				t.Fatal(err)
			}
			_ = os.Remove(filepath.Join(n.Dir, "observed.json"))
			if readErr != nil || !bytes.Equal(data, want) {
				t.Fatalf("coverage assignment order differs: retained=%s (%v), Go=%s", data, readErr, want)
			}
		})
	}
}

func TestRetainedCoverageAuxiliaryMetadataExcludesUnselectedDependencies(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and executes Go fixture packages")
	}
	neutralAmbient(t)
	n := retainedFixtureInvocation(t, map[string]string{
		"dep/dep.go":  "package dep\nfunc Value() int{return 42}\n",
		"a/a.go":      "package a\nimport \"retained.test/dep\"\nfunc Value() int{return dep.Value()}\n",
		"a/a_test.go": "package a\nimport \"testing\"\nfunc TestA(t *testing.T){if Value()!=42{t.Fatal(\"value\")}}\n",
		"b/b.go":      "package b\nfunc Value() int{return 7}\n",
		"b/b_test.go": "package b\nimport \"testing\"\nfunc TestB(t *testing.T){if Value()!=7{t.Fatal(\"value\")}}\n",
	})
	retainedSetFlags(n, "-coverpkg=./... -coverprofile=coverage.txt")
	n.CacheBypass = true
	pkgs := []string{"retained.test/a", "retained.test/b"}
	profile, err := openRetainedCoverage(n)
	if err != nil {
		t.Fatal(err)
	}
	s := buildRetainedScope(t.Context(), n, pkgs, retainedProcessHooks{})
	defer s.Close()
	if s.Err != nil {
		t.Fatal(s.Err)
	}
	artifacts := []*retainedArtifact{s.Packages[pkgs[0]].Artifact, s.Packages[pkgs[1]].Artifact}
	percent := func(text string) string {
		for line := range strings.Lines(text) {
			if strings.HasPrefix(line, "coverage:") {
				return line
			}
		}
		return ""
	}
	gotPercent := map[string]string{}
	for _, pkg := range pkgs {
		r := retainedRuntimeFor(t, n, s.Packages[pkg].Artifact, pkg, nil, profile, artifacts...)
		result := runRetained(t.Context(), t.Context(), n, r, &stipulatorv1.ProducerIdentity{}, retainedProcessHooks{})
		if !result.HarnessComplete() || result.ProfileErr != nil {
			t.Fatalf("%s: %+v", pkg, result)
		}
		gotPercent[pkg] = percent(result.Stream.pkgOutput.b.String())
	}
	if err := profile.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(n.Dir, "coverage.txt"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := ownedRunner.Run(t.Context(), n.Dir, witnessEnvOf(n), "test", "-json", "-count=1", "-timeout=0", pkgs[0], pkgs[1])
	if err != nil {
		t.Fatalf("Go oracle: %v; %s", err, out)
	}
	want, err := os.ReadFile(filepath.Join(n.Dir, "coverage.txt"))
	if err != nil {
		t.Fatal(err)
	}
	canonical := func(b []byte) string {
		lines := strings.Split(strings.TrimSpace(string(b)), "\n")
		sort.Strings(lines)
		return strings.Join(lines, "\n")
	}
	if canonical(got) != canonical(want) {
		t.Fatalf("auxiliary coverage scope differs\nretained:\n%s\nGo:\n%s", got, want)
	}
	dec := json.NewDecoder(bytes.NewReader(out))
	wantPercent := map[string]string{}
	for {
		var event struct{ Package, Output string }
		err := dec.Decode(&event)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if p := percent(event.Output); p != "" {
			wantPercent[event.Package] = p
		}
	}
	for _, pkg := range pkgs {
		if gotPercent[pkg] == "" || gotPercent[pkg] != wantPercent[pkg] {
			t.Errorf("%s percentage: retained=%q Go=%q", pkg, gotPercent[pkg], wantPercent[pkg])
		}
	}
}

func TestRetainedProfilingCompanionMatchesGo(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and executes Go fixture packages")
	}
	neutralAmbient(t)
	n := retainedFixtureInvocation(t, map[string]string{"renamed/p_test.go": "package different\nimport \"testing\"\nfunc TestOK(t *testing.T){}\n"})
	suffix := ".test"
	if runtime.GOOS == "windows" {
		suffix += ".exe"
	}
	defaultName := "renamed" + suffix
	for _, tc := range []struct {
		name, flags, target string
		raw                 []string
		exists              bool
	}{
		{"memory", "-memprofile=heap.out", defaultName, nil, true},
		{"alias", "-test.memprofile=heap.out", defaultName, nil, true},
		{"profile-outputdir", "-memprofile=heap.out -outputdir=profiles", defaultName, nil, true},
		{"explicit-output", "-memprofile=heap.out -o=bin/custom", "bin/custom", nil, true},
		{"existing-directory", "-o=bin", "bin/" + defaultName, nil, true},
		{"new-directory", "-o=new-bin/", "new-bin/" + defaultName, nil, true},
		{"null-output", "-memprofile=heap.out -o=" + os.DevNull, defaultName, nil, false},
		{"trace-only", "-trace=trace.out", defaultName, nil, false},
		{"raw-profile", "", defaultName, []string{"-test.memprofile=raw.out"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			nc := *n
			retainedSetFlags(&nc, tc.flags)
			nc.Args = tc.raw
			nc.CacheBypass = true
			for _, dir := range []string{"profiles", "bin"} {
				if err := os.MkdirAll(filepath.Join(n.Dir, dir), 0755); err != nil {
					t.Fatal(err)
				}
			}
			public := filepath.Join(n.Dir, tc.target)
			_ = os.Remove(public)
			_ = os.Remove(filepath.Join(n.Dir, defaultName))
			if out, err := ownedRunner.Run(t.Context(), nc.Dir, witnessEnvOf(&nc), testCommandArgs(&nc, "retained.test/renamed", nil, "")...); err != nil {
				t.Fatalf("Go oracle: %v; %s", err, out)
			}
			_, err := os.Stat(public)
			if (err == nil) != tc.exists {
				t.Fatalf("Go companion %s: %v; expected exists=%v", public, err, tc.exists)
			}
			_ = os.Remove(public)
			if tc.name == "new-directory" {
				if err := os.Remove(filepath.Dir(public)); err != nil {
					t.Fatal(err)
				}
			}
			a := retainedBuildOK(t, &nc, "retained.test/renamed")
			r := retainedRuntimeFor(t, &nc, a, "retained.test/renamed", nil, nil)
			result := runRetained(t.Context(), t.Context(), &nc, r, &stipulatorv1.ProducerIdentity{}, retainedProcessHooks{})
			if !result.HarnessComplete() || result.ProfileErr != nil {
				t.Fatalf("retained attempt: %+v", result)
			}
			private, err := os.ReadFile(a.Path)
			if err != nil {
				t.Fatal(err)
			}
			if err := a.Close(); err != nil {
				t.Fatal(err)
			}
			if err := r.Close(); err != nil {
				t.Fatal(err)
			}
			copied, err := os.ReadFile(public)
			if !tc.exists {
				if !os.IsNotExist(err) {
					t.Fatalf("unexpected companion %s: %v", public, err)
				}
				return
			}
			if err != nil || !bytes.Equal(private, copied) {
				t.Fatalf("public companion was lost or differs from the executed artifact: %v", err)
			}
			cmd, err := ownedRunner.Program(t.Context(), n.PkgDirs["retained.test/renamed"], witnessEnvOf(&nc), public, "-test.run=TestOK", "-test.count=1")
			if err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			cmd.Stdout = &output
			cmd.Stderr = &output
			process, err := startRetainedCommand(t.Context(), cmd, "binary", retainedProcessHooks{})
			if err != nil {
				t.Fatal(err)
			}
			if err := process.Wait(); err != nil {
				t.Fatalf("public companion is unusable after private cleanup: %v; %s", err, output.String())
			}
		})
	}
}

func TestRetainedCompanionPreservesNonObjectDestination(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and executes Go fixture packages")
	}
	neutralAmbient(t)
	n := retainedFixtureInvocation(t, map[string]string{"p_test.go": "package p\nimport \"testing\"\nfunc TestOK(t *testing.T){}\n"})
	retainedSetFlags(n, "-memprofile=heap.out -o=keep.txt")
	n.CacheBypass = true
	destination := filepath.Join(n.Dir, "keep.txt")
	if err := os.WriteFile(destination, []byte("user text\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ownedRunner.Run(t.Context(), n.Dir, witnessEnvOf(n), testCommandArgs(n, "retained.test", nil, "")...); err == nil {
		t.Fatal("Go oracle unexpectedly overwrote non-object output")
	}
	a := retainedBuildOK(t, n, "retained.test")
	r := retainedRuntimeFor(t, n, a, "retained.test", nil, nil)
	result := runRetained(t.Context(), t.Context(), n, r, &stipulatorv1.ProducerIdentity{}, retainedProcessHooks{})
	data, err := os.ReadFile(destination)
	if err != nil || string(data) != "user text\n" {
		t.Fatalf("non-object destination changed: %q (%v)", data, err)
	}
	if !result.HarnessComplete() || result.ProfileErr == nil || result.Classify(n, "retained.test").disposition != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_DEGRADED {
		t.Fatalf("output failure must preserve binary facts and refuse health: %+v", result)
	}
}
