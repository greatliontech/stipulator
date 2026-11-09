package trace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func RefreshFromEnv() {
	Refresh(os.Getenv(EnvTrace), os.Getenv(EnvTraceFile))
}

type panicOnFormatError struct{}

func (panicOnFormatError) Error() string {
	panic("Error() must not be called on disabled fast path")
}

func TestDisabledFastPathZeroAllocs(t *testing.T) {
	t.Setenv(EnvTrace, "")
	t.Setenv(EnvTraceFile, "")
	RefreshFromEnv()

	ctx := t.Context()
	if Enabled(ctx, DomainCompile, "corpus") {
		t.Fatal("expected Enabled to be false when tracing is off")
	}

	flags := []string{"-race", "-trimpath"}
	var lazyErr error = panicOnFormatError{}
	allocs := testing.AllocsPerRun(100, func() {
		_ = Enabled(ctx, DomainWitness, "pkg.TestFoo")
		Emit(ctx, DomainWitness, "solo_exec", "pkg.TestFoo",
			String("invocation", "race"),
			Int("count", 3),
			Bool("served", false),
			Strings("flags", flags),
			Err(lazyErr),
		)
	})
	if allocs != 0 {
		t.Fatalf("expected 0 allocations on disabled fast path, got %v", allocs)
	}
}

func TestFilterGrammarAndContextSinks(t *testing.T) {
	t.Setenv(EnvTrace, "")
	t.Setenv(EnvTraceFile, "")
	RefreshFromEnv()

	for _, inactiveSpec := range []string{"", "  ", "0", "false", "off", "none", "unknown_domain"} {
		ctx := WithFilteredSink(context.Background(), inactiveSpec, NewRecorder())
		if Enabled(ctx, DomainCompile, "corpus") {
			t.Fatalf("spec %q should not enable tracing", inactiveSpec)
		}
	}

	for _, allSpec := range []string{"1", "true", "ALL", "*", "all:"} {
		ctx, cancel := context.WithCancel(context.Background())
		rec := NewRecorder()
		tctx := WithFilteredSink(ctx, allSpec, rec)
		for _, dom := range []Domain{DomainCompile, DomainDiscover, DomainResolve, DomainWitness, DomainVerify} {
			if !Enabled(tctx, dom, "any") {
				t.Fatalf("spec %q did not enable domain %s", allSpec, dom)
			}
			Emit(tctx, dom, "evt", "any")
		}
		if got := len(rec.Events()); got != 5 {
			t.Fatalf("spec %q recorded %d events, want 5", allSpec, got)
		}
		cancel()
		if Enabled(tctx, DomainCompile, "any") {
			t.Fatalf("spec %q remained enabled after context cancellation", allSpec)
		}
	}

	parentCtx, cancelParent := context.WithCancel(context.Background())
	defer cancelParent()
	parentRec := NewRecorder()
	pctx := WithFilteredSink(parentCtx, "witness:TestPWD,resolve:encoding/json,compile", parentRec)

	childCtx, cancelChild := context.WithCancel(pctx)
	childRec := NewRecorder()
	cctx := WithFilteredSink(childCtx, "*:SpecialTarget", childRec)

	Emit(cctx, DomainWitness, "solo_exec", "example.com/p.TestPWD", String("inv", "race"))
	Emit(cctx, DomainWitness, "solo_exec", "example.com/p.TestOther", String("inv", "race"))
	Emit(cctx, DomainResolve, "serve_selection", "encoding/json", Bool("served", true))
	Emit(cctx, DomainResolve, "serve_selection", "fmt", Bool("served", true))
	Emit(cctx, DomainCompile, "prepare_done", "dir", Int("problems", 0))
	Emit(cctx, DomainVerify, "verify_correlate", "example.com/p.SpecialTarget", String("status", "ok"))

	if got := len(parentRec.Events()); got != 3 {
		t.Fatalf("parent recorder got %d events, want 3: %+v", got, parentRec.Events())
	}
	if got := len(childRec.Events()); got != 1 || childRec.Events()[0].Target != "example.com/p.SpecialTarget" {
		t.Fatalf("child recorder got %+v, want 1 SpecialTarget event", childRec.Events())
	}

	cancelChild()
	if Enabled(cctx, DomainVerify, "example.com/p.SpecialTarget") {
		t.Fatal("cancelled child sink remained enabled while parent sink was active")
	}
	Emit(cctx, DomainVerify, "verify_correlate", "example.com/p.SpecialTarget", String("status", "stale"))
	if got := len(childRec.Events()); got != 1 {
		t.Fatalf("cancelled child sink received event after cancellation: %+v", childRec.Events())
	}

	cancelParent()
	if Enabled(pctx, DomainCompile, "dir") || AnyEnabled() {
		t.Fatal("expected AnyEnabled() == false immediately after cancelling all context sinks")
	}
	runtime.GC()
}

func TestRecorderAccessorsAndDeepCopy(t *testing.T) {
	t.Setenv(EnvTrace, "")
	RefreshFromEnv()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rec := NewRecorder()
	tctx := WithSink(ctx, rec)

	tags := []string{"a", "b"}
	Emit(tctx, DomainDiscover, "load_capture", "/repo",
		String("goversion", "go1.26.0"),
		Int("count", 7),
		Int64("big", 42),
		Uint32("mode", 0o644),
		Bool("warm", true),
		Strings("flags", tags),
		Err(nil),
		Err(errors.New("bad package")),
	)
	tags[0] = "mutated"

	evts := rec.ByDomain(DomainDiscover)
	if len(evts) != 1 {
		t.Fatalf("expected 1 discover event, got %d", len(evts))
	}
	ev := evts[0]
	if got := ev.StringField("goversion"); got != "go1.26.0" {
		t.Fatalf("StringField(goversion) = %q, want go1.26.0", got)
	}
	if got, ok := ev.IntField("count"); !ok || got != 7 {
		t.Fatalf("IntField(count) = %d, %v", got, ok)
	}
	if got, ok := ev.IntField("big"); !ok || got != 42 {
		t.Fatalf("IntField(big) = %d, %v", got, ok)
	}
	if got, ok := ev.IntField("mode"); !ok || got != 0o644 {
		t.Fatalf("IntField(mode) = %d, %v", got, ok)
	}
	if got, ok := ev.BoolField("warm"); !ok || !got {
		t.Fatalf("BoolField(warm) = %v, %v", got, ok)
	}
	if got := ev.StringsField("flags"); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("StringsField(flags) = %v, want [a b]", got)
	}
	if got := ev.StringField("error"); got != "bad package" {
		t.Fatalf("StringField(error) = %q, want 'bad package'", got)
	}
	if len(rec.ByName("load_capture")) != 1 {
		t.Fatalf("ByName(load_capture) count != 1")
	}
	rec.Reset()
	if len(rec.Events()) != 0 {
		t.Fatalf("expected empty recorder after Reset")
	}
}

func TestFormatTextAndJSONL(t *testing.T) {
	ev := Event{
		Time:   time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC),
		PID:    1234,
		Domain: DomainWitness,
		Name:   "finish_native_run",
		Target: "example.com/pkg.TestFoo",
		Fields: []Field{
			String("invocation", "race"),
			String("reason", "contains space=and\"quote"),
			String("empty", ""),
			Int("pass", 2),
			Bool("solo", true),
			Strings("items", []string{"x", "y"}),
		},
	}

	textLine := string(FormatText(ev))
	wantPrefix := `gofresh/trace pid=1234 domain=witness event=finish_native_run target=example.com/pkg.TestFoo invocation=race reason="contains space=and\"quote" empty="" pass=2 solo=true items=x,y` + "\n"
	if textLine != wantPrefix {
		t.Fatalf("FormatText mismatch:\ngot:  %q\nwant: %q", textLine, wantPrefix)
	}

	jsonlLine := FormatJSONL(ev)
	if !bytes.HasSuffix(jsonlLine, []byte("\n")) {
		t.Fatalf("FormatJSONL missing trailing newline: %q", string(jsonlLine))
	}
	var parsed jsonlEvent
	if err := json.Unmarshal(jsonlLine, &parsed); err != nil {
		t.Fatalf("FormatJSONL produced invalid JSON: %v (%s)", err, string(jsonlLine))
	}
	if parsed.PID != 1234 || parsed.Domain != "witness" || parsed.Event != "finish_native_run" || parsed.Target != "example.com/pkg.TestFoo" {
		t.Fatalf("unexpected parsed JSONL header: %+v", parsed)
	}
	if parsed.Fields["invocation"] != "race" || parsed.Fields["solo"] != true {
		t.Fatalf("unexpected parsed JSONL fields: %+v", parsed.Fields)
	}
}

func TestEnvFileAndConcurrentEmission(t *testing.T) {
	tmp := t.TempDir()
	textPath := filepath.Join(tmp, "trace.log")
	jsonlPath := filepath.Join(tmp, "trace.jsonl")

	t.Cleanup(RefreshFromEnv)
	t.Setenv(EnvTrace, "discover,witness")
	t.Setenv(EnvTraceFile, textPath)
	RefreshFromEnv()

	const workers = 8
	const perWorker = 25
	var wg sync.WaitGroup
	for w := range workers {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := range perWorker {
				Emit(context.Background(), DomainWitness, "solo_exec", "pkg.Sym",
					Int("worker", id),
					Int("iter", i),
					String("status", "valid"),
				)
				Emit(context.Background(), DomainCompile, "prepare_done", "pkg.Sym")
			}
		}(w)
	}
	wg.Wait()

	data, err := os.ReadFile(textPath)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", textPath, err)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) != workers*perWorker {
		t.Fatalf("expected %d lines in %s, got %d", workers*perWorker, textPath, len(lines))
	}
	for i, line := range lines {
		if !strings.HasPrefix(line, "gofresh/trace pid=") || !strings.Contains(line, "domain=witness event=solo_exec") {
			t.Fatalf("line %d corrupted or unexpected: %q", i, line)
		}
	}

	t.Setenv(EnvTrace, "all")
	t.Setenv(EnvTraceFile, jsonlPath)
	RefreshFromEnv()

	Emit(context.Background(), DomainDiscover, "load_capture", "repo", Bool("audited", true))

	jdata, err := os.ReadFile(jsonlPath)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", jsonlPath, err)
	}
	var parsed jsonlEvent
	if err := json.Unmarshal(jdata, &parsed); err != nil {
		t.Fatalf("Unmarshal JSONL file: %v (%s)", err, string(jdata))
	}
	if parsed.Domain != "discover" || parsed.Event != "load_capture" || parsed.Fields["audited"] != true {
		t.Fatalf("unexpected JSONL event: %+v", parsed)
	}

	Refresh("all", filepath.Join(tmp, "nonexistent_dir", "trace.jsonl"))
	cfg := envCfg.Load()
	fallbackJSONL := cfg != nil && cfg.jsonl
	if fallbackJSONL {
		t.Fatal("failed OpenFile on .jsonl path must fall back to text format (jsonl=false) on stderr")
	}
}
