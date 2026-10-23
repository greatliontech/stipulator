package test2json

import (
	"bytes"
	"encoding/json"
	"io"
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"
)

// Raw events matter here: error and error-continue boundaries must not be
// hidden by the corpus comparison's adjacent-output normalization.
func rawPartitionEvents(t *testing.T, input string, parts []int) []jsonEvent {
	t.Helper()
	var out bytes.Buffer
	c := NewConverter(&out, "", 0)
	for _, size := range append(parts, len(input)) {
		size = min(size, len(input))
		if _, err := c.Write([]byte(input[:size])); err != nil {
			t.Fatal(err)
		}
		input = input[size:]
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	var result []jsonEvent
	d := json.NewDecoder(&out)
	for {
		var e jsonEvent
		if err := d.Decode(&e); err == io.EOF {
			return result
		} else if err != nil {
			t.Fatal(err)
		}
		result = append(result, e)
	}
}

func TestErrorMarkerRemainderPartitions(t *testing.T) {
	output := func(kind, text string) jsonEvent {
		e := jsonEvent{"Action": "output", "Output": text}
		if kind != "" {
			e["OutputType"] = kind
		}
		return e
	}
	for _, tc := range []struct {
		name, input string
		want        []jsonEvent
	}{
		{"begin-escape", "\x0f\x1b\x16x\x0e\n", []jsonEvent{output("error", "\x16x\n")}},
		{"begin-escape-prefix", "p\x0f\x1b\x16x\x0eq\n", []jsonEvent{output("", "p"), output("error", "\x16x"), output("", "q\n")}},
		{"end-escape", "\x0fx\x0e\x1b\x16y\n", []jsonEvent{output("error", "x"), output("", "\x16y\n")}},
		{"end-escape-prefix", "p\x0fx\x0e\x1b\x16y\n", []jsonEvent{output("", "p"), output("error", "x"), output("", "\x16y\n")}},
		{"empty-error", "\x0f\x0e\n", []jsonEvent{output("error", "\n")}},
		{"empty-error-eof", "\x0f\x0e", []jsonEvent{output("error", "")}},
		{"empty-error-then-escape", "\x0f\x0e\x1b\x16x\n", []jsonEvent{output("error", ""), output("", "\x16x\n")}},
		{"bare-end-escape", "\x0e\x1b\x16x\n", []jsonEvent{output("", ""), output("", "\x16x\n")}},
		{"adjacent-errors", "\x0fa\x0e\x0fb\x0e\n", []jsonEvent{output("error", "a"), output("error", "b\n")}},
		{"nested-begin", "\x0f\x0fx\x0e\n", []jsonEvent{output("error", "x\n")}},
		{"nested-end", "\x0fx\x0e\x0e\n", []jsonEvent{output("error", "x"), output("", "\n")}},
		{"quoted-end", "\x0f\x1b\x0ex\x0e\n", []jsonEvent{output("error", "\x0ex\n")}},
		{"quoted-begin-after-end", "\x0fx\x0e\x1b\x0fy\n", []jsonEvent{output("error", "x"), output("", "\x0fy\n")}},
		{"quoted-begin", "p\x1b\x0fq\n", []jsonEvent{output("", "p\x0fq\n")}},
		{"continued-error", "p\x0fa\n\x1b\x16b\x0e\n", []jsonEvent{output("", "p"), output("error", "a\n"), output("error-continue", "\x16b\n")}},
		{"begin-at-buffer-end", strings.Repeat("p", 1023) + "\x0f\x1b\x16x\x0e\n", []jsonEvent{output("", strings.Repeat("p", 1023)), output("error-continue", "\x16x\n")}},
		{"begin-escape-at-buffer-end", strings.Repeat("p", 1022) + "\x0f\x1b\x16x\x0e\n", []jsonEvent{output("", strings.Repeat("p", 1022)), output("error-continue", "\x16x\n")}},
		{"utf8-begin-escape-edge", strings.Repeat("λ", 511) + "\x0f\x1b\x16x\x0e\n", []jsonEvent{output("", strings.Repeat("λ", 511)), output("error-continue", "\x16x\n")}},
		{"end-at-buffer-end", "\x0f" + strings.Repeat("x", 1022) + "\x0e\x1b\x16q\n", []jsonEvent{output("error", strings.Repeat("x", 1022)), output("", "\x16q\n")}},
		{"end-escape-at-buffer-end", "\x0f" + strings.Repeat("x", 1021) + "\x0e\x1b\x16q\n", []jsonEvent{output("error", strings.Repeat("x", 1021)), output("", "\x16q\n")}},
		{"nested-begin-at-buffer-end", strings.Repeat("p", 1023) + "\x0f\x0fx\x0e\n", []jsonEvent{output("", strings.Repeat("p", 1023)), output("error", "x\n")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := append([]jsonEvent{{"Action": "start"}}, tc.want...)
			check := func(parts []int) {
				t.Helper()
				if got := rawPartitionEvents(t, tc.input, parts); !reflect.DeepEqual(got, want) {
					t.Fatalf("parts=%v\ngot=%v\nwant=%v", parts, got, want)
				}
			}
			for split := 0; split <= len(tc.input); split++ {
				check([]int{split})
			}
			check(bytesToParts(len(tc.input), 1))
			rng := rand.New(rand.NewPCG(5, 9))
			for range 30 {
				var parts []int
				for left := len(tc.input); left > 0; {
					n := 1 + rng.IntN(left)
					parts = append(parts, n)
					left -= n
				}
				check(parts)
			}
		})
	}
}
