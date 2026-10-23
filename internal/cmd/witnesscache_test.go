package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pb "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/backends/golang"
	"github.com/greatliontech/stipulator/internal/progress"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/internal/views"
	"github.com/greatliontech/stipulator/internal/wire"
	"github.com/greatliontech/stipulator/stipulate"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func TestWitnessCacheHumanRenderingBoundsDetails(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-cache-refusals")
	a := &pb.WitnessCacheLoadAccount{}
	a.SetOmitted(9)
	a.SetIncomplete(true)
	a.SetProblem("listing stopped")
	a.SetProblemTruncated(true)
	for i := range 25 {
		r := &pb.WitnessCacheRefusal{}
		r.SetFile(fmt.Sprintf("cache-file-%02d", i))
		r.SetDetail(fmt.Sprintf("detail-%02d", i))
		r.SetDetailTruncated(i == 0)
		r.SetFilenameTruncated(i == 1)
		r.SetCoordinateOmitted(i == 2)
		r.SetCause(pb.WitnessCacheRefusalCause_WITNESS_CACHE_REFUSAL_CAUSE_READ)
		if i == 3 {
			c := &pb.WitnessCacheCoordinate{}
			c.SetGroup("group")
			c.SetPackage("example.com/p")
			c.SetTest("TestExample")
			r.SetCoordinate(c)
		}
		a.SetRefusals(append(a.GetRefusals(), r))
	}
	before := proto.CloneOf(a)
	var out bytes.Buffer
	renderWitnessCacheLoad(&out, a)
	text := out.String()
	if strings.Count(text, "cache-file-") != 5 || !strings.Contains(text, "34") || !strings.Contains(text, "29 further") || !strings.Contains(text, "incomplete") {
		t.Fatalf("human account lost bound/count/partial state: %s", text)
	}
	for _, word := range []string{"listing stopped", "detail-04", "TestExample", "example.com/p", "group=group", "coordinate omitted"} {
		if !strings.Contains(text, word) {
			t.Fatalf("missing %q: %s", word, text)
		}
	}
	if strings.Contains(text, "cache-file-05") || strings.Count(text, "(truncated)") != 3 || !proto.Equal(a, before) {
		t.Fatalf("render mutated account or lost truncation semantics: %s", text)
	}
	var stdout, stderr bytes.Buffer
	res := &pb.CheckResult{}
	res.SetWitnessCacheLoad(a)
	renderCheck(&stdout, &stderr, res)
	if strings.Count(stderr.String(), "witness cache:") != 1 || strings.Count(stderr.String(), "cache-file-") != 5 {
		t.Fatalf("check did not render one bounded account: %s", stderr.String())
	}
	out.Reset()
	renderWitnessCacheLoad(&out, nil)
	if out.Len() != 0 {
		t.Fatal("clean load rendered an advisory")
	}
	a = &pb.WitnessCacheLoadAccount{}
	a.SetIncomplete(true)
	a.SetProblem("cannot list store")
	renderWitnessCacheLoad(&out, a)
	if !strings.Contains(out.String(), "0 observed") || !strings.Contains(out.String(), "cannot list store") || !strings.Contains(out.String(), "incomplete") {
		t.Fatalf("account-level failure disappeared: %s", out.String())
	}
}

// witnessAccountCLI supplies one captured pass to the real command path.
func witnessAccountCLI(t *testing.T) (string, *verify.TestRun, *int) {
	t.Helper()
	neutralAmbient(t)
	dir := t.TempDir()
	for path, text := range map[string]string{
		"go.mod":                         "module example.com/cacheaccount\n\ngo 1.26.4\n",
		"a.go":                           "package cacheaccount\n",
		"specs/a.md":                     "# A\n\n**REQ-acc-may** (behavior): The fixture MAY pass.\n",
		".stipulator/manifest.textproto": "include: \"specs/**/*.md\"\n",
		".stipulator/policy.textproto":   "invocations { name: \"all\" timeout { seconds: 300 } go { packages: \"./...\" race: true } }\n",
	} {
		p := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	a := &pb.WitnessCacheLoadAccount{}
	a.SetOmitted(4)
	for i := range 25 {
		r := &pb.WitnessCacheRefusal{}
		r.SetFile(fmt.Sprintf("refused-%02d", i))
		a.SetRefusals(append(a.GetRefusals(), r))
	}
	priorDir, priorRun, priorBackends := chdir, runWitnessesPolicy, servingBackends
	t.Cleanup(func() { chdir, runWitnessesPolicy, servingBackends = priorDir, priorRun, priorBackends })
	chdir = dir
	calls := 0
	tr := &verify.TestRun{WitnessCacheLoad: a, SelectiveServing: true}
	runWitnessesPolicy = func(context.Context, *golang.Capture, verify.WitnessSeeding) (*verify.TestRun, error) {
		calls++
		return tr, nil
	}
	servingBackends = func(context.Context, string, []string) (map[string]verify.Backend, error) {
		return map[string]verify.Backend{}, nil
	}
	return dir, tr, &calls
}

func TestWitnessCacheCLIJSONUsesSharedProjection(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-cache-refusals", "REQ-mcp-surfaces")
	_, tr, calls := witnessAccountCLI(t)
	a := tr.WitnessCacheLoad
	for _, tc := range []struct {
		command func() *cobra.Command
		view    string
		message interface {
			proto.Message
			GetWitnessCacheLoad() *pb.WitnessCacheLoadAccount
		}
		full bool
	}{
		{verifyCmd, "summary", &pb.VerifySummary{}, false},
		{verifyCmd, "bindings", &pb.VerifyReport{}, true},
		{gateCmd, "summary", &pb.CoverageSummary{}, false},
		{gateCmd, "full", &pb.CoverageReport{}, true},
	} {
		before := *calls
		var output string
		var err error
		stderr := stderrOf(t, func() {
			output, err = captureStdout(t, func() error {
				c := tc.command()
				c.SetArgs([]string{"--json", "--view", tc.view})
				return c.ExecuteContext(t.Context())
			})
		})
		if err != nil {
			t.Fatalf("CLI %s: %v; %s", tc.view, err, stderr)
		}
		if err := protojson.Unmarshal([]byte(output), tc.message); err != nil {
			t.Fatal(err)
		}
		want := views.WitnessCacheLoadSummary(a)
		if tc.full {
			want = a
		}
		if !proto.Equal(tc.message.GetWitnessCacheLoad(), want) {
			t.Fatalf("%s lost shared projection: %s", tc.view, output)
		}
		canonical, err := wire.CanonicalJSON(tc.message)
		if err != nil || output != string(canonical) {
			t.Fatalf("noncanonical CLI projection: %v", err)
		}
		if *calls != before+1 || strings.Count(stderr, "witness cache:") != 1 || strings.Count(stderr, "refused-") != 5 {
			t.Fatalf("CLI extra pass or duplicate/unbounded human account: calls=%d; %s", *calls-before, stderr)
		}
	}
}

func TestGateQuietSuppressesWitnessOutputWithoutChangingEvidence(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-cache-refusals", "REQ-mcp-surfaces")
	dir, tr, calls := witnessAccountCLI(t)
	tr.Ran, tr.Fresh, tr.Uncached, tr.OutsidePolicy = 2, 3, 1, 4
	tr.Degraded = "freshness fault"
	tr.ExecutedReasons = map[string]string{"example.com/p.TestRun": "input moved"}
	tr.UncacheableReasons = map[string]string{"example.com/p.TestRun": "publication refused"}
	tr.Failures = map[string]string{"example.com/p.TestFailed": "retained failure body\n"}
	diagnostic := &pb.FailureDiagnostic{}
	diagnostic.SetPackage("example.com/denied")
	diagnostic.SetOutput("retained package denial\n")
	tr.Diagnostics = []*pb.FailureDiagnostic{diagnostic}
	account := proto.CloneOf(tr.WitnessCacheLoad)
	for _, must := range []bool{false, true} {
		keyword := "MAY"
		if must {
			keyword = "MUST"
		}
		if err := os.WriteFile(filepath.Join(dir, "specs/a.md"), []byte("# A\n\n**REQ-acc-may** (behavior): The fixture "+keyword+" pass.\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		for _, quiet := range []bool{false, true} {
			before := *calls
			var stdout string
			var runErr error
			var status bytes.Buffer
			stderr := stderrOf(t, func() {
				stdout, runErr = captureStdout(t, func() error {
					args := []string{"-C", dir, "gate"}
					if quiet {
						args = append(args, "--quiet")
					}
					return execute(t.Context(), args, progress.Stderr(&status), &status)
				})
			})
			var verdict ExitStatus
			if must {
				if !errors.As(runErr, &verdict) || verdict.Code != 1 {
					t.Fatalf("quiet=%v: expected gate failure, got %v", quiet, runErr)
				}
			} else if runErr != nil {
				t.Fatalf("quiet=%v: expected pass, got %v", quiet, runErr)
			}
			if *calls != before+1 || !proto.Equal(account, tr.WitnessCacheLoad) || tr.Ran != 2 || tr.Fresh != 3 || len(tr.Failures) != 1 || len(tr.Diagnostics) != 1 {
				t.Fatal("rendering changed evidence or ran the pass again")
			}
			if quiet {
				if stdout != "" || stderr != "" || status.Len() != 0 {
					t.Errorf("quiet normal verdict leaked output: stdout=%q stderr=%q progress=%q", stdout, stderr, status.String())
				}
			} else {
				if strings.Count(stderr, "witness cache:") != 1 || strings.Count(stderr, "refused-") != 5 || !strings.Contains(stderr, "retained failure body") || !strings.Contains(stderr, "retained package denial") || !strings.Contains(stderr, "witnessing:") || !strings.Contains(stderr, "witnessed:") || status.Len() == 0 || stdout == "" {
					t.Fatalf("nonquiet positive control lost rendering: %q %q %q", stdout, stderr, status.String())
				}
			}
		}
	}
}

func TestGateQuietPreservesOperationalEndings(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-surfaces")
	for _, interrupted := range []bool{false, true} {
		t.Run(fmt.Sprint(interrupted), func(t *testing.T) {
			dir, _, _ := witnessAccountCLI(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			fault := errors.New("witness operation fault")
			runWitnessesPolicy = func(context.Context, *golang.Capture, verify.WitnessSeeding) (*verify.TestRun, error) {
				if interrupted {
					cancel()
					return nil, ctx.Err()
				}
				return nil, fault
			}
			var events []*pb.ProgressEvent
			var status bytes.Buffer
			var runErr error
			stderrOf(t, func() {
				_, runErr = captureStdout(t, func() error {
					return execute(ctx, []string{"-C", dir, "gate", "--quiet"}, func(e *pb.ProgressEvent) { events = append(events, e) }, &status)
				})
			})
			cause := pb.TerminalCause_TERMINAL_CAUSE_SERVER_FAILURE
			if interrupted {
				cause = pb.TerminalCause_TERMINAL_CAUSE_CANCELLED
				var ending Interrupted
				if !errors.As(runErr, &ending) || !errors.Is(runErr, context.Canceled) {
					t.Fatalf("interruption changed: %v", runErr)
				}
			} else if !errors.Is(runErr, fault) || status.Len() == 0 {
				t.Fatalf("operational error or its account lost: %v, %q", runErr, status.String())
			}
			if len(events) != 1 || events[0].GetTerminalCause() != cause {
				t.Fatalf("quiet fault ending lost or live progress leaked: %v", events)
			}
		})
	}
}

func TestGateQuietSuppressesCompileDiagnostics(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-surfaces")
	for _, tc := range []struct {
		name, document, diagnostic string
		fails                      bool
	}{
		{"term-warning", "# T\n\n**law node** (term): a node holding law.\n\n**node** (term): a graph vertex.\n\n**REQ-acc-may** (behavior): The fixture MAY pass.\n", "node", false},
		{"duplicate-id", "# T\n\n**REQ-acc-dup** (behavior): The fixture MAY pass.\n\n**REQ-acc-dup** (behavior): The fixture MAY pass twice.\n", "REQ-acc-dup", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, tr, calls := witnessAccountCLI(t)
			account := proto.CloneOf(tr.WitnessCacheLoad)
			if err := os.WriteFile(filepath.Join(dir, ".stipulator/manifest.textproto"), []byte("include: \"specs/**/*.md\"\nterm_lint { warn_shadowing: true denylist: \"node\" }\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "specs/a.md"), []byte(tc.document), 0o644); err != nil {
				t.Fatal(err)
			}
			for _, quiet := range []bool{false, true} {
				before := *calls
				var stdout string
				var runErr error
				var status bytes.Buffer
				stderr := stderrOf(t, func() {
					stdout, runErr = captureStdout(t, func() error {
						args := []string{"-C", dir, "gate"}
						if quiet {
							args = append(args, "--quiet")
						}
						return execute(t.Context(), args, progress.Stderr(&status), &status)
					})
				})
				wantCalls := 1
				if tc.fails {
					wantCalls = 0
					var verdict ExitStatus
					if !errors.As(runErr, &verdict) || verdict.Code != 1 {
						t.Fatalf("quiet=%v: invalid corpus returned %v", quiet, runErr)
					}
				} else if runErr != nil {
					t.Fatalf("quiet=%v: warning failed the verdict: %v", quiet, runErr)
				}
				if *calls-before != wantCalls || !proto.Equal(account, tr.WitnessCacheLoad) {
					t.Fatal("quiet changed preparation or evidence collection")
				}
				if quiet {
					if stdout != "" || stderr != "" || status.Len() != 0 {
						t.Errorf("quiet compile verdict leaked output: stdout=%q stderr=%q progress=%q", stdout, stderr, status.String())
					}
				} else if !strings.Contains(stderr, "specs/a.md") || !strings.Contains(stderr, tc.diagnostic) {
					t.Fatalf("ordinary command lost compiler diagnostic: %q", stderr)
				}
			}
		})
	}
}
