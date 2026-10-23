// Package golang implements Go policy execution and witness attribution.
//
// Retained execution builds a test artifact under its compilation description
// and runs that exact artifact under its package-runtime description. Each
// attempt keeps its own producer identity and native transaction. The owned
// test-output converter emits JSON consumed by the existing event correlator;
// conversion success, binary exit, harness completion and native capture
// receipts remain independent facts. EOF first preserves diagnostic fragments,
// then delivers pending per-test reports without synthesizing package health.
//
// Only the retained binary has a runtime process boundary. Its stdout and stderr
// share an owned pipe whose EOF includes descendant holds. Envelope expiry
// requests a dump and allows its grace interval; caller cancellation escalates
// immediately. Output-processing failure stops the owned process tree and
// refuses a complete stream. No converter process, synthetic framing or output
// spool participates in completion.
package golang
