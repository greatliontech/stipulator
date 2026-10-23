package test2json

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type jsonEvent map[string]any

func events(t *testing.T, data []byte) []jsonEvent {
	t.Helper()
	var out []jsonEvent
	d := json.NewDecoder(bytes.NewReader(data))
	for {
		var e jsonEvent
		if err := d.Decode(&e); err == io.EOF {
			return out
		} else if err != nil {
			t.Fatal(err)
		}
		// JSON output chunk boundaries are not payload boundaries. Preserve
		// their attribution and output kind while comparing concatenated bytes.
		if len(out) > 0 && e["Action"] == "output" {
			last := out[len(out)-1]
			if last["Action"] == "output" && last["Test"] == e["Test"] && last["OutputType"] == e["OutputType"] {
				last["Output"] = last["Output"].(string) + e["Output"].(string)
				continue
			}
		}
		out = append(out, e)
	}
}

func decodeParts(t *testing.T, input string, parts []int) []jsonEvent {
	t.Helper()
	var out bytes.Buffer
	c := NewConverter(&out, "", 0)
	for _, size := range parts {
		if size > len(input) {
			size = len(input)
		}
		p := []byte(input[:size])
		if n, err := c.Write(p); err != nil || n != len(p) {
			t.Fatalf("write=%d %v", n, err)
		}
		// The converter must not retain aliases to a caller's write buffer.
		for i := range p {
			p[i] = 'Z'
		}
		input = input[size:]
	}
	if _, err := c.Write([]byte(input)); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	first := out.String()
	if err := c.Close(); err != nil || out.String() != first {
		t.Fatal("Close duplicated output", err)
	}
	return events(t, out.Bytes())
}

func TestUpstreamGoldenCorpus(t *testing.T) {
	files, err := filepath.Glob("testdata/*.test")
	if err != nil || len(files) != 19 {
		t.Fatalf("corpus=%d: %v", len(files), err)
	}
	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			input, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			golden, err := os.ReadFile(strings.TrimSuffix(file, ".test") + ".json")
			if err != nil {
				t.Fatal(err)
			}
			// These upstream files end with a complete report but no package
			// terminal. The corrected EOF delivers that report, and only it.
			if strings.HasSuffix(file, "/framefail.test") || strings.HasSuffix(file, "/frameescape.test") {
				golden = append(golden, []byte("{\"Action\":\"fail\",\"Test\":\"TestAscii\"}\n")...)
			}
			want := events(t, golden)
			if strings.HasSuffix(file, "/attr.test") {
				// This upstream golden predates OutputType. Every output in
				// this fixture is an actual framing line; assert its kind too.
				for _, e := range want {
					if e["Action"] == "output" {
						e["OutputType"] = "frame"
					}
				}
			}
			for _, width := range []int{len(input) + 1, 1, 2, 17, 4096} {
				var parts []int
				for i := 0; i < len(input); i += width {
					parts = append(parts, width)
				}
				got := decodeParts(t, string(input), parts)
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("width=%d\ngot=%v\nwant=%v", width, got, want)
				}
			}
		})
	}
}

func TestProtocolPartitions(t *testing.T) {
	for _, tc := range []struct {
		name, input, output, outcomes, terminal string
	}{
		{"escaped-pass", "\x1b\x16PASS\n", "\x16PASS\n", "", ""},
		{"partial-pass", "PASS", "PASS", "", ""},
		{"partial-report", "--- FAIL: TestFalse (0.00s)", "--- FAIL: TestFalse (0.00s)", "", ""},
		{"partial-framed-report", "\x16--- PASS: TestFalse", "\x16--- PASS: TestFalse", "", ""},
		{"pending-report", "=== RUN   TestA\n--- FAIL: TestA (0.00s)\ntail", "=== RUN   TestA\n--- FAIL: TestA (0.00s)\ntail", "fail:TestA", ""},
		{"nested-report", "--- FAIL: TestA\n    --- FAIL: TestA/child\ntail", "--- FAIL: TestA\n    --- FAIL: TestA/child\ntail", "fail:TestA/child,fail:TestA", ""},
		{"real-pass-partial", "\x16PASS\ntail", "PASS\ntail", "", "pass"},
		{"real-marker", "PASS\x16tail", "PASS\x16tail", "", "pass"},
		{"escaped-escape", "\x1b\x1b\x1b\x16PASS\n", "\x1b\x16PASS\n", "", ""},
		{"escape-newline", "\x1b\n\x16PASS\n", "\nPASS\n", "", "pass"},
		{"utf8", "λ☺😂\x1b\x16tail", "λ☺😂\x16tail", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			check := func(parts []int) {
				t.Helper()
				got := decodeParts(t, tc.input, parts)
				var output, terminal string
				var outcomes []string
				for _, e := range got {
					if s, ok := e["Output"].(string); ok {
						output += s
					}
					if e["Action"] == "pass" || e["Action"] == "fail" || e["Action"] == "skip" {
						if name, ok := e["Test"].(string); ok {
							outcomes = append(outcomes, e["Action"].(string)+":"+name)
						} else {
							terminal = e["Action"].(string)
						}
					}
				}
				if output != tc.output || terminal != tc.terminal || strings.Join(outcomes, ",") != tc.outcomes {
					t.Fatalf("parts=%v: output=%q terminal=%q outcomes=%v; want %q %q %q", parts, output, terminal, outcomes, tc.output, tc.terminal, tc.outcomes)
				}
			}
			for split := 0; split <= len(tc.input); split++ {
				check([]int{split})
			}
			check(bytesToParts(len(tc.input), 1))
			rng := rand.New(rand.NewPCG(1, 2))
			for range 100 {
				var parts []int
				for n := len(tc.input); n > 0; {
					k := 1 + rng.IntN(n)
					parts = append(parts, k)
					n -= k
				}
				check(parts)
			}
		})
	}
}

func bytesToParts(n, width int) []int {
	var parts []int
	for i := 0; i < n; i += width {
		parts = append(parts, width)
	}
	return parts
}

func TestBufferEdgesPreservePendingReports(t *testing.T) {
	for _, size := range []int{4095, 4096, 4097, 8192, 32768} {
		for _, suffix := range []string{"\x16partial", "\x1b\x16partial", "λ☺😂", "\npartial"} {
			input := "--- FAIL: TestA\n" + strings.Repeat("x", size) + suffix
			baseline := decodeParts(t, input, nil)
			var baselineOutput string
			for _, e := range baseline {
				if s, ok := e["Output"].(string); ok {
					baselineOutput += s
				}
			}
			if strings.Contains(baselineOutput, "=== NAME") {
				t.Fatal("synthetic framing leaked")
			}
			wantOutput := "--- FAIL: TestA\n" + strings.Repeat("x", size) + strings.Replace(suffix, "\x1b", "", 1)
			if baselineOutput != wantOutput {
				t.Fatalf("size=%d suffix=%q changed output payload", size, suffix)
			}
			last := baseline[len(baseline)-1]
			if last["Action"] != "fail" || last["Test"] != "TestA" {
				t.Fatalf("pending report lost: %v", last)
			}
			for _, width := range []int{1, 17, 4095, 4096} {
				if got := decodeParts(t, input, bytesToParts(len(input), width)); !reflect.DeepEqual(got, baseline) {
					t.Fatalf("size=%d suffix=%q width=%d changed events", size, suffix, width)
				}
			}
			rng := rand.New(rand.NewPCG(uint64(size), 3))
			for range 20 {
				var parts []int
				for n := len(input); n > 0; {
					k := 1 + rng.IntN(min(n, 4097))
					parts = append(parts, k)
					n -= k
				}
				if got := decodeParts(t, input, parts); !reflect.DeepEqual(got, baseline) {
					t.Fatalf("size=%d suffix=%q random partition changed events", size, suffix)
				}
			}
		}
	}
}

func TestLongOutputHasBoundedState(t *testing.T) {
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "absent"))
	c := NewConverter(io.Discard, "", 0)
	chunk := bytes.Repeat([]byte("x"), 4096)
	for range 16384 {
		if _, err := c.Write(chunk); err != nil {
			t.Fatal(err)
		}
		if cap(c.input.b) != inBuffer || cap(c.output.b) != outBuffer || len(c.report) != 0 {
			t.Fatal("output grew retained state")
		}
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestPartialOutputFlushResumesScanning(t *testing.T) {
	var out bytes.Buffer
	c := NewConverter(&out, "", 0)
	if _, err := c.Write([]byte("\x16=== RUN   TestA\nlong partial diagnostic\x16=== NAME  TestB\nx\n")); err != nil {
		t.Fatal(err)
	}
	got := events(t, out.Bytes())
	last := got[len(got)-1]
	if last["Action"] != "output" || last["Test"] != "TestB" || last["Output"] != "x\n" {
		t.Fatalf("complete output was held after partial flush: %v", got)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRealFramingAfterFullFragment(t *testing.T) {
	for _, size := range []int{4095, 4096, 8192} {
		prefix := "\x16=== RUN   TestA\n" + strings.Repeat("x", size)
		input := prefix + "\x16--- PASS: TestA\n\x16PASS\n"
		wantOutput := "=== RUN   TestA\n" + strings.Repeat("x", size) + "--- PASS: TestA\nPASS\n"
		for _, width := range []int{1, 17, 4096, len(input)} {
			got := decodeParts(t, input, bytesToParts(len(input), width))
			var output string
			passed := 0
			for _, e := range got {
				if s, ok := e["Output"].(string); ok {
					output += s
				}
				if e["Action"] == "pass" && e["Test"] == "TestA" {
					passed++
				}
			}
			last := got[len(got)-1]
			if output != wantOutput || passed != 1 || last["Action"] != "pass" || last["Test"] != nil {
				t.Fatalf("size=%d width=%d: real framing lost (passed=%d terminal=%v)", size, width, passed, last)
			}
		}
	}
}

type failingWriter struct {
	err   error
	short bool
}

func (w failingWriter) Write(p []byte) (int, error) {
	if w.short {
		return len(p) - 1, nil
	}
	return 0, w.err
}

func TestWriterFailureIsSticky(t *testing.T) {
	fault := errors.New("sink failed")
	for _, w := range []failingWriter{{err: fault}, {short: true}} {
		c := NewConverter(w, "", 0)
		want := fault
		if w.short {
			want = io.ErrShortWrite
		}
		if _, err := c.Write([]byte("PASS\n")); !errors.Is(err, want) {
			t.Fatalf("write=%v", err)
		}
		if err := c.Close(); !errors.Is(err, want) {
			t.Fatalf("close=%v", err)
		}
		if _, err := c.Write([]byte("FAIL\n")); !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("write after close=%v", err)
		}
	}
}

func FuzzConversionPartitions(f *testing.F) {
	for _, seed := range []string{"\x1b\x16PASS\n", "--- FAIL: TestA\npartial", "\x16PASS", "λ☺😂\x1b\x16tail", strings.Repeat("x", 4096) + "\x16partial", "\x0f\x1b\x16x\x0e\n", "\x0fx\x0e\x1b\x16y\n", "\x0f\x0f\x0e\x0e\n"} {
		f.Add([]byte(seed), uint16(1))
	}
	f.Fuzz(func(t *testing.T, input []byte, width uint16) {
		baseline := decodeParts(t, string(input), nil)
		got := decodeParts(t, string(input), bytesToParts(len(input), 1+int(width)))
		if !reflect.DeepEqual(got, baseline) {
			t.Fatalf("partition width %d changes events", 1+int(width))
		}
	})
}
