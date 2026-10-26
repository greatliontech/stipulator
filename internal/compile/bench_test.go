package compile

import (
	"testing"
	"testing/fstest"
)

func BenchmarkCompile(b *testing.B) {
	fsys := fstest.MapFS{
		".stipulator/manifest.textproto": {Data: []byte("include: \"specs/**/*.md\"\n")},
		"specs/core.md": {Data: []byte(`# Core Specification

## Terminology

**Term — Corpus**: The set of markdown documents compiled into the IR.

**Term — Witness**: A test or benchmark bound to a requirement.

## Requirements

**REQ-core-parse** (behavior): The compiler MUST parse every included document in the **corpus**.

**REQ-core-resolve** (invariant, depends REQ-core-parse): Every **witness** binding MUST resolve against the **corpus** IR.

**REQ-core-wire** (wire, refines REQ-core-resolve): The serialized IR MUST preserve canonical requirement ordering:

- deterministic requirement order
- deterministic edge order
`)},
		"specs/evidence.md": {Data: []byte(`# Evidence Specification

## Verification

**REQ-ev-fresh** (behavior, depends REQ-core-resolve): Verification MUST serve cached **witness** verdicts when the **corpus** and source closure match.

**REQ-ev-failclosed** (invariant, refines REQ-ev-fresh): An unreadable cache entry MUST NOT be served as a valid **witness**.
`)},
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		spec, diags, err := Compile(fsys)
		if err != nil || len(Errors(diags)) != 0 || len(spec.GetRequirements()) != 5 {
			b.Fatalf("Compile = (%v, %v, %v)", spec, diags, err)
		}
	}
}
