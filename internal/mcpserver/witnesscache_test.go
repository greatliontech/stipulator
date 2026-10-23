package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/greatliontech/gofresh"
	pb "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/backends/golang"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/internal/views"
	"github.com/greatliontech/stipulator/stipulate"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func refusalAccount() *pb.WitnessCacheLoadAccount {
	a := &pb.WitnessCacheLoadAccount{}
	a.SetOmitted(7)
	a.SetIncomplete(true)
	a.SetProblem("enumeration interrupted")
	a.SetProblemTruncated(true)
	for i := range 25 {
		r := &pb.WitnessCacheRefusal{}
		r.SetFile(fmt.Sprintf("refused-variant-%02d", i))
		r.SetCause(pb.WitnessCacheRefusalCause_WITNESS_CACHE_REFUSAL_CAUSE_ENVELOPE)
		r.SetDetail("refusal detail must stay structured")
		r.SetDetailTruncated(true)
		a.SetRefusals(append(a.GetRefusals(), r))
	}
	return a
}

type accountMessage interface {
	proto.Message
	GetWitnessCacheLoad() *pb.WitnessCacheLoadAccount
	ClearWitnessCacheLoad()
}

func decodeAccountResult(t *testing.T, res *mcp.CallToolResult, m accountMessage) {
	t.Helper()
	if res.IsError {
		t.Fatalf("tool refused: %s", toolText(t, res))
	}
	b, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if err := protojson.Unmarshal(b, m); err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(b), `"witnessCacheLoad"`) > 1 {
		t.Fatalf("duplicate account homes: %s", b)
	}
}

func TestWitnessCacheAccountAcrossServedOperations(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-cache-refusals", "REQ-mcp-response-contract")
	for _, tc := range []struct {
		name, tool string
		args       map[string]any
		message    func() accountMessage
		full       bool
		export     func() accountMessage
	}{
		{"verify", "verify", nil, func() accountMessage { return &pb.VerifySummary{} }, false, nil},
		{"verify-empty-scope", "verify", map[string]any{"path": "absent"}, func() accountMessage { return &pb.VerifySummary{} }, false, nil},
		{"verify-bindings", "verify", map[string]any{"view": "bindings"}, func() accountMessage { return &pb.VerifyReport{} }, true, nil},
		{"gate", "gate", nil, func() accountMessage { return &pb.CoverageSummary{} }, false, nil},
		{"gate-full", "gate", map[string]any{"view": "full"}, func() accountMessage { return &pb.CoverageReport{} }, true, nil},
		{"gate-empty-scope", "gate", map[string]any{"path": "absent", "view": "full"}, func() accountMessage { return &pb.CoverageReport{} }, true, nil},
		{"context", "context", map[string]any{"ids": "REQ-m-a"}, func() accountMessage { return &pb.DossierReport{} }, false, nil},
		{"context-export", "context", map[string]any{"ids": "REQ-m-a", "export_path": ".stipulator/exports/context.json"}, func() accountMessage { return &pb.ExportResult{} }, false, func() accountMessage { return &pb.DossierReport{} }},
		{"partitions", "partitions", nil, func() accountMessage { return &pb.PartitionReport{} }, false, nil},
		{"partitions-export", "partitions", map[string]any{"export_path": ".stipulator/exports/partitions.json"}, func() accountMessage { return &pb.ExportResult{} }, false, func() accountMessage { return &pb.PartitionReport{} }},
		{"gap-list", "gap", map[string]any{"list": true}, func() accountMessage { return &pb.GapListResult{} }, false, nil},
		{"prune-preview", "prune", map[string]any{"check": true}, func() accountMessage { return &pb.WriteResult{} }, false, nil},
		{"prune-apply", "prune", nil, func() accountMessage { return &pb.WriteResult{} }, false, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := refusalAccount()
			original := proto.CloneOf(a)
			calls := 0
			sess, writes := harnessWith(t, map[string]string{
				".stipulator/bindings/a.textproto": pinnedBinding(t),
				".stipulator/gaps/m-a.textproto":   "requirement_id: \"REQ-m-a\"\nreason: \"pending\"\nlands { manual { condition: \"judged done\" } }\n",
			}, func(s *Server) {
				s.runTests = func(context.Context, *golang.Capture, verify.WitnessSeeding, map[gofresh.Subject]bool) (*verify.TestRun, error) {
					calls++
					return &verify.TestRun{WitnessCacheLoad: a, RaceEnabled: true, SelectiveServing: true, Fresh: 1, Outcomes: map[string]verify.TestOutcome{"example.com/p.TestA": verify.TestPassed}}, nil
				}
			})
			res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{Name: tc.tool, Arguments: tc.args})
			if err != nil {
				t.Fatal(err)
			}
			m := tc.message()
			decodeAccountResult(t, res, m)
			want := views.WitnessCacheLoadSummary(a)
			if tc.full {
				want = a
			}
			if !proto.Equal(m.GetWitnessCacheLoad(), want) {
				t.Fatalf("account lost or incorrectly bounded: %v", m.GetWitnessCacheLoad())
			}
			text := toolText(t, res)
			if strings.Count(text, "witnessCacheLoad") != 1 || !strings.Contains(text, "32") || !strings.Contains(text, "incomplete") || strings.Contains(text, "refused-variant") || strings.Contains(text, "refusal detail") {
				t.Fatalf("digest does not point once to structured account: %s", text)
			}
			if tc.export != nil {
				exported := tc.export()
				path := tc.args["export_path"].(string)
				if err := protojson.Unmarshal(writes[path], exported); err != nil {
					t.Fatal(err)
				}
				if !proto.Equal(exported.GetWitnessCacheLoad(), a) {
					t.Fatal("export lost full account")
				}
			}
			if calls != 1 || !proto.Equal(a, original) {
				t.Fatalf("projection ran work or mutated source: calls=%d", calls)
			}
			if tc.export == nil {
				// Removing only the advisory metadata cannot move any verdict,
				// tally, gap state, component or witness fact on the same pass.
				a = nil
				baseline, err := sess.CallTool(t.Context(), &mcp.CallToolParams{Name: tc.tool, Arguments: tc.args})
				if err != nil {
					t.Fatal(err)
				}
				plain := tc.message()
				decodeAccountResult(t, baseline, plain)
				m.ClearWitnessCacheLoad()
				if !proto.Equal(m, plain) || calls != 2 {
					t.Fatalf("advisory metadata changed operation facts: %v versus %v; calls=%d", m, plain, calls)
				}
			}
		})
	}
}

func TestWitnessCacheCheckViewsAndIncompleteOnly(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-cache-refusals", "REQ-mcp-views-check-summary")
	for _, account := range []*pb.WitnessCacheLoadAccount{refusalAccount(), pb.WitnessCacheLoadAccount_builder{Incomplete: proto.Bool(true), Problem: proto.String("cannot list store")}.Build(), nil} {
		for _, view := range []string{"summary", "full"} {
			calls := 0
			want := &pb.CheckResult{}
			want.SetPassed(true)
			want.SetWitnessCacheLoad(account)
			sess, _ := checkHarness(t, func(context.Context, bool, []string) (*pb.CheckResult, error) { calls++; return want, nil })
			res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{Name: "check", Arguments: map[string]any{"view": view}})
			if err != nil {
				t.Fatal(err)
			}
			var m accountMessage = &pb.CheckSummary{}
			if view == "full" {
				m = &pb.CheckResult{}
			}
			decodeAccountResult(t, res, m)
			expected := account
			if view == "summary" {
				expected = views.WitnessCacheLoadSummary(account)
			}
			if !proto.Equal(m.GetWitnessCacheLoad(), expected) || calls != 1 {
				t.Fatalf("check account/calls = %v/%d", m.GetWitnessCacheLoad(), calls)
			}
			if account != nil && !strings.Contains(toolText(t, res), "witnessCacheLoad") {
				t.Fatal("account absent from text digest")
			}
			if account == nil && strings.Contains(toolText(t, res), "witnessCacheLoad") {
				t.Fatal("invented account")
			}
		}
	}
}

func TestWitnessCacheNoLoadOperationsHaveNoAccount(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-cache-refusals")
	for _, tc := range []struct {
		tool               string
		args               map[string]any
		gapWithoutSubjects bool
	}{
		{"verify", map[string]any{"no_test": true}, false},
		{"context", map[string]any{"ids": "REQ-m-a", "no_test": true}, false},
		{"partitions", map[string]any{"no_test": true}, false},
		{"gap", map[string]any{"list": true}, false},
		{"prune", map[string]any{"check": true}, false},
		{"prune", map[string]any{"dangling": true}, false},
		{"gap", map[string]any{"list": true}, true},
		{"prune", map[string]any{"check": true}, true},
	} {
		t.Run(tc.tool+fmt.Sprint(tc.args), func(t *testing.T) {
			var files map[string]string
			if tc.gapWithoutSubjects {
				files = map[string]string{".stipulator/gaps/m-a.textproto": "requirement_id: \"REQ-m-a\"\nreason: \"pending\"\nlands { manual { condition: \"judged done\" } }\n"}
			}
			sess, _ := harnessWith(t, files, func(s *Server) {
				s.runTests = func(context.Context, *golang.Capture, verify.WitnessSeeding, map[gofresh.Subject]bool) (*verify.TestRun, error) {
					t.Error("unexpected witness run")
					return &verify.TestRun{}, nil
				}
			})
			res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{Name: tc.tool, Arguments: tc.args})
			if err != nil || res.IsError {
				t.Fatalf("%s: %v %v", tc.tool, err, res)
			}
			b, _ := json.Marshal(res.StructuredContent)
			if strings.Contains(string(b)+toolText(t, res), "witnessCacheLoad") {
				t.Fatal("invented load account")
			}
		})
	}
}
