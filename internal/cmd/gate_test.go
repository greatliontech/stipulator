package cmd

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
)

func TestGateJSONIgnoresConsumerFindings(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":                         "module example.com/gatefixture\n\ngo 1.26.4\n",
		"gate.go":                        "package gatefixture\n\nfunc F() {}\n",
		"gate_test.go":                   "package gatefixture\n\nimport \"testing\"\n\nfunc TestF(t *testing.T) { F() }\n",
		"specs/gate.md":                  "# Gate\n\n**REQ-cli-gate** (behavior): The gate MAY pass.\n",
		".stipulator/manifest.textproto": "include: \"specs/**/*.md\"\n",
		".gomutant/findings.json":        "{not json}",
	}
	for path, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	priorDir := chdir
	chdir = dir
	t.Cleanup(func() { chdir = priorDir })
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	priorStdout := os.Stdout
	os.Stdout = write
	t.Cleanup(func() { os.Stdout = priorStdout })

	cmd := gateCmd()
	cmd.SetArgs([]string{"--json"})
	runErr := cmd.ExecuteContext(context.Background())
	if err := write.Close(); err != nil {
		t.Fatal(err)
	}
	os.Stdout = priorStdout
	out, err := io.ReadAll(read)
	if err != nil {
		t.Fatal(err)
	}
	if runErr != nil {
		t.Fatalf("gate: %v\n%s", runErr, out)
	}
	summary := &stipulatorv1.CoverageSummary{}
	if err := protojson.Unmarshal(out, summary); err != nil {
		t.Fatalf("gate output is not a strict CoverageSummary: %v\n%s", err, out)
	}
	if !summary.GetGatePasses() || summary.GetExempt() != 1 {
		t.Fatalf("gate summary = %v", summary)
	}
}
