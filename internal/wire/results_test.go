package wire

import (
	"encoding/json"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/stipulate"
)

// Every tool result is one projection of its wire message: the CLI's
// canonical JSON and the structured tool result decode to the same
// message, for the write-shaped, compile, list, chain, bundle, and
// export results as for the reports (REQ-mcp-tools).
func TestEveryResultRendersThroughTheOneProjection(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-tools")
	compile := &stipulatorv1.CompileResult{}
	compile.SetDiagnostics([]string{"a.md:3: unknown term"})
	compile.SetDiagnosticsOmitted(2)
	compile.SetRequirements(7)
	write := &stipulatorv1.WriteResult{}
	write.SetWrote([]string{".stipulator/bindings/a.textproto"})
	write.SetNotes([]string{"retargeted"})
	write.SetCheck(true)
	row := &stipulatorv1.GapReport{}
	row.SetRequirementId("REQ-a")
	row.SetState(stipulatorv1.GapState_GAP_STATE_OPEN)
	list := &stipulatorv1.GapListResult{}
	list.SetGaps([]*stipulatorv1.GapReport{row})
	list.SetGapsOmitted(3)
	link := &stipulatorv1.ExplainLink{}
	link.SetKind("refusal")
	link.SetPackage("example.com/p")
	chain := &stipulatorv1.ExplainResult{}
	chain.SetArm("environment-audit")
	chain.SetLinks([]*stipulatorv1.ExplainLink{link})
	bundle := &stipulatorv1.ReadSpecResult{}
	bundle.SetSpec("# A\n")
	export := &stipulatorv1.ExportResult{}
	export.SetExported(".stipulator/exports/x.json")
	export.SetBytes(12)
	for _, tc := range []struct {
		name string
		m    proto.Message
		want proto.Message
		key  string
	}{
		{"compile", compile, &stipulatorv1.CompileResult{}, `"diagnosticsOmitted":2`},
		{"write", write, &stipulatorv1.WriteResult{}, `"check":true`},
		{"gap list", list, &stipulatorv1.GapListResult{}, `"gapsOmitted":3`},
		{"explain", chain, &stipulatorv1.ExplainResult{}, `"arm":"environment-audit"`},
		{"read_spec", bundle, &stipulatorv1.ReadSpecResult{}, `"spec":"# A\n"`},
		{"export", export, &stipulatorv1.ExportResult{}, `"bytes":12`},
	} {
		cli, err := CanonicalJSON(tc.m)
		if err != nil {
			t.Fatal(err)
		}
		structured, err := StructuredContent(tc.m)
		if err != nil {
			t.Fatal(err)
		}
		mcpBytes, err := json.Marshal(structured)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(mcpBytes), tc.key) {
			t.Fatalf("%s: projection lacks %s: %s", tc.name, tc.key, mcpBytes)
		}
		fromCLI := proto.Clone(tc.want)
		if err := protojson.Unmarshal(cli, fromCLI); err != nil {
			t.Fatalf("%s: CLI projection is not strict: %v\n%s", tc.name, err, cli)
		}
		fromMCP := proto.Clone(tc.want)
		if err := protojson.Unmarshal(mcpBytes, fromMCP); err != nil {
			t.Fatalf("%s: MCP projection is not strict: %v\n%s", tc.name, err, mcpBytes)
		}
		if !proto.Equal(fromCLI, tc.m) || !proto.Equal(fromMCP, tc.m) {
			t.Fatalf("%s: the surfaces decode different messages:\ncli: %s\nmcp: %s", tc.name, cli, mcpBytes)
		}
	}
}

// The gap list's write fields are the write result's, field for field:
// a write field the list lacks would silently never reach a listing.
func TestGapListCarriesEveryWriteField(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-tools")
	write := (&stipulatorv1.WriteResult{}).ProtoReflect().Descriptor().Fields()
	list := (&stipulatorv1.GapListResult{}).ProtoReflect().Descriptor().Fields()
	for i := 0; i < write.Len(); i++ {
		f := write.Get(i)
		g := list.ByName(f.Name())
		if g == nil || g.Kind() != f.Kind() || g.Cardinality() != f.Cardinality() {
			t.Fatalf("GapListResult lacks WriteResult's %s (%v)", f.Name(), f.Kind())
		}
	}
}

// The canonical projection is ProtoJSON's encoding re-serialized for
// determinism alone: the characters an HTML-minded encoder would
// escape stay as ProtoJSON wrote them.
func TestCanonicalJSONKeepsProtoJSONsCharacters(t *testing.T) {
	stipulate.Covers(t, "REQ-report-check-result")
	m := &stipulatorv1.WriteResult{}
	m.SetNotes([]string{"run `stipulator retarget --from <old> --to <new>` & re-pin"})
	out, err := CanonicalJSON(m)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "\\u003c") || !strings.Contains(string(out), "--from <old> --to <new>` & re-pin") {
		t.Fatalf("canonical projection escaped ProtoJSON's characters:\n%s", out)
	}
}

// The canonical form's shape is literal: sorted keys, two-space
// indentation, one trailing newline (REQ-report-check-result).
func TestCanonicalJSONShapeIsLiteral(t *testing.T) {
	stipulate.Covers(t, "REQ-report-check-result")
	m := &stipulatorv1.WriteResult{}
	m.SetNotes([]string{"n"})
	m.SetCheck(true)
	got, err := CanonicalJSON(m)
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"check\": true,\n  \"notes\": [\n    \"n\"\n  ]\n}\n"
	if string(got) != want {
		t.Fatalf("canonical form drifted:\n%q\nwant:\n%q", got, want)
	}
}
