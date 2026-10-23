package golang

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/greatliontech/gofresh/gotool"
	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
)

// This is a differential against cmd/go running a REAL test binary. TestMain
// records its incoming argv before parsing, so even admitted selection aliases
// and raw unknown/positional arguments are observed without running benchmarks
// or starting a fuzz campaign. The oracle is cmd/go, not our projection table.
func TestRetainedArgumentsMatchGo(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and executes Go fixture packages")
	}
	neutralAmbient(t)
	n := retainedFixtureInvocation(t, map[string]string{"p_test.go": `package p
import ("encoding/json"; "os"; "testing")
func TestMain(m *testing.M) { b,_:=json.Marshal(os.Args[1:]); if err:=os.WriteFile(os.Getenv("ARGS_FILE"),b,0600);err!=nil{panic(err)}; os.Exit(0) }
`})
	argFile := filepath.Join(n.Dir, "argv.json")
	n.Env = gotool.SetEnv(n.Env, "ARGS_FILE", argFile)
	n.WitnessEnv = gotool.SetEnv(witnessEnvOf(n), "ARGS_FILE", argFile)
	retainedSetFlags(n, "-cover")
	a := retainedBuildOK(t, n, "retained.test")
	// Every forwarded flag in the selected Go release, exercised through its
	// currently admitted spelling (policy's alias handling remains independent).
	cases := []struct {
		name, flags string
		raw         []string
		count       int32
		bypass      bool
		selection   []string
	}{
		{name: "all", flags: "-artifacts=false -test.bench=BenchmarkNo -benchmem -test.benchtime=2x -blockprofile=block.out -blockprofilerate=3 -test.count=02 -coverprofile=cover.out -cpu=1,2 -cpuprofile=cpu.out -test.failfast -fullpath -fuzzminimizetime=2x -test.fuzztime=1x -test.list=No -memprofile=mem.out -memprofilerate=5 -mutexprofile=mutex.out -mutexprofilefraction=7 -outputdir=profiles -parallel=0x2 -test.run=No -test.short -shuffle=0007 -test.skip=No -test.timeout=1m -trace=trace.out -v"},
		{name: "fuzz-alias", flags: "-test.fuzz=FuzzNo -test.fuzztime=1x"},
		{name: "shared-alias-values", flags: "-parallel=1 -test.parallel=3 -parallel=2 -v=false -test.v=true -test.count=8 -test.timeout=1m -test.run=Wrong", count: 3, selection: []string{"TestLiteral.[]", "FuzzA"}},
		{name: "bypass", flags: "-test.count=8", count: 4, bypass: true},
		{name: "quoted", flags: `'-test.run=space and \\literal' "-test.skip=other space" '-outputdir=directory with spaces' -test.v=false`},
		{name: "implicit-profile-dir", flags: "-memprofile=heap.out -artifacts"},
		{name: "raw-precedence", flags: "-parallel=2 -test.short", raw: []string{"-test.parallel=3", "-test.parallel", "4", "-test.v=false", "-test.short=false"}},
		{name: "raw-positionals", flags: "-parallel=2", raw: []string{"positional", "-test.parallel=9", "--", "-unknown=verbatim"}},
		{name: "raw-terminator", raw: []string{"--", "-test.timeout=1ns", "--unknown", "value"}},
		{name: "empty-outputdir", flags: "-outputdir= -memprofile=x"},
		{name: "absolute-outputdir", flags: "-outputdir=" + n.Dir + "/unused/../canonical"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			nc := *n
			nc.Args, nc.Count, nc.CacheBypass = tc.raw, tc.count, tc.bypass
			retainedSetFlags(&nc, tc.flags)
			if err := validateGoflags(tc.flags); err != nil {
				t.Fatalf("differential case must be policy-admitted: %v", err)
			}
			flags, err := retainedGOFLAGS(&nc)
			if err != nil {
				t.Fatal(err)
			}
			// This binary only records argv, so reuse the same test executable;
			// cover instrumentation's execution is covered in the profile test.
			profile, err := openRetainedCoverage(&nc)
			if err != nil {
				if err := os.MkdirAll(flags.outputDir(&nc), 0755); err != nil {
					t.Fatal(err)
				}
				profile, err = openRetainedCoverage(&nc)
			}
			if err != nil {
				t.Fatal(err)
			}
			defer profile.Close()
			r := retainedRuntimeFor(t, &nc, a, "retained.test", tc.selection, profile)
			got := runRetained(t.Context(), t.Context(), &nc, r, &stipulatorv1.ProducerIdentity{}, retainedProcessHooks{})
			if got.BinaryErr != nil || got.ConversionErr != nil {
				t.Fatalf("retained: %v / %v", got.BinaryErr, got.ConversionErr)
			}
			readArgs := func() []string {
				t.Helper()
				b, err := os.ReadFile(argFile)
				if err != nil {
					t.Fatal(err)
				}
				var args []string
				if err := json.Unmarshal(b, &args); err != nil {
					t.Fatal(err)
				}
				return args
			}
			actual := readArgs()
			if err := os.Remove(argFile); err != nil {
				t.Fatal(err)
			}
			out, err := ownedRunner.Run(t.Context(), nc.Dir, witnessEnvOf(&nc), testCommandArgs(&nc, "retained.test", tc.selection, "")...)
			if err != nil {
				t.Fatalf("Go oracle: %v; %s", err, out)
			}
			want := readArgs()
			clean := func(args []string) []string {
				var result []string
				for _, arg := range args {
					if strings.HasPrefix(arg, "-test.testlogfile=") || strings.HasPrefix(arg, "-test.gocoverdir=") {
						continue
					}
					if strings.HasPrefix(arg, "-test.coverprofile=") {
						arg = "-test.coverprofile=<private-fragment>"
					}
					result = append(result, arg)
				}
				return result
			}
			if !reflect.DeepEqual(clean(actual), clean(want)) {
				t.Errorf("binary argv differs\nretained: %q\ncmd/go:   %q", actual, want)
			}
		})
	}
}

func TestRetainedGOFLAGSQuotingMatchesGoFailures(t *testing.T) {
	for _, s := range []string{"'-parallel=2", `"-parallel=2`} {
		if _, err := splitRetainedGOFLAGS(s); err == nil {
			t.Errorf("unterminated GOFLAGS accepted: %q", s)
		}
	}
}
