package harden

import (
	"encoding/json"
	"testing"
)

// TestExportTargets pins the targets export (REQ-harden-export): the
// versioned envelope, symbol/witnesses/requirements per entry, witness-less
// targets included, symbol-less refused.
//
//gofresh:pure
func TestExportTargets(t *testing.T) {
	doc, err := ExportTargets([]Target{
		{Symbol: "example.com/p.F", Witnesses: []string{"example.com/p.TestF"}, Requirements: []string{"REQ-a"}},
		{Symbol: "example.com/p.G"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Version int `json:"stipulatorTargets"`
		Targets []struct {
			Symbol       string   `json:"symbol"`
			Witnesses    []string `json:"witnesses"`
			Requirements []string `json:"requirements"`
		} `json:"targets"`
	}
	if err := json.Unmarshal(doc, &got); err != nil {
		t.Fatal(err)
	}
	if got.Version != 1 || len(got.Targets) != 2 {
		t.Fatalf("envelope = %+v", got)
	}
	if got.Targets[0].Witnesses[0] != "example.com/p.TestF" || got.Targets[0].Requirements[0] != "REQ-a" {
		t.Fatalf("entry = %+v", got.Targets[0])
	}
	if got.Targets[1].Symbol != "example.com/p.G" || len(got.Targets[1].Witnesses) != 0 {
		t.Fatalf("witness-less entry = %+v", got.Targets[1])
	}
	if _, err := ExportTargets([]Target{{}}); err == nil {
		t.Fatal("symbol-less target exported")
	}
	// Deterministically ordered: an export commits and diffs stably.
	again, err := ExportTargets([]Target{
		{Symbol: "example.com/p.F", Witnesses: []string{"example.com/p.TestF"}, Requirements: []string{"REQ-a"}},
		{Symbol: "example.com/p.G"},
	})
	if err != nil || string(again) != string(doc) {
		t.Fatalf("export not stable: %v", err)
	}
}
