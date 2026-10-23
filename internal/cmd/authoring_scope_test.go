package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/greatliontech/stipulator/internal/verify"
)

type authoringBackend func(string) (verify.Resolution, string, error)

func (f authoringBackend) Resolve(s string) (verify.Resolution, string, error) { return f(s) }

func TestCLIExplicitBindCompleteScopeAndAtomicFailure(t *testing.T) {
	for _, mode := range []string{"success", "late failure", "cancel", "unsupported", "empty"} {
		t.Run(mode, func(t *testing.T) {
			write := scaffoldCorpus(t)
			write("docs/specs/a.md", "# A\n\n**REQ-aa-a** (behavior): It MUST a.\n\n**REQ-aa-b** (behavior): It MUST b.\n")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var got []string
			closed, calls := false, 0
			declarationBackends = func(_ context.Context, _ string, symbols []string) (map[string]verify.Backend, func() error, error) {
				got = symbols
				return map[string]verify.Backend{"go": authoringBackend(func(symbol string) (verify.Resolution, string, error) {
					calls++
					if calls == 2 && mode == "late failure" {
						return verify.NotFound, "", fmt.Errorf("selected declaration failed")
					}
					if calls == 2 && mode == "cancel" {
						cancel()
					}
					return verify.Resolved, strings.Repeat("a", 64), nil
				})}, func() error { closed = true; return nil }, nil
			}
			cmd := bindCmd()
			args := []string{"--req", "REQ-aa-a", "--req", "REQ-aa-b", "--symbol", "example.com/p.A", "--symbol", "example.com/q.B", "--role", "implements"}
			if mode == "unsupported" {
				args = append(args, "--backend", "unknown")
			}
			if mode == "empty" {
				args = []string{}
			}
			cmd.SetArgs(args)
			err := cmd.ExecuteContext(ctx)
			if mode == "success" && err != nil || mode != "success" && err == nil {
				t.Fatalf("result: %v", err)
			}
			if mode == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation: %v", err)
			}
			if mode != "empty" && !closed {
				t.Fatal("backend not closed")
			}
			if mode == "empty" {
				if got != nil {
					t.Fatal("empty batch constructed backend")
				}
				return
			}
			want := []string{"example.com/p.A", "example.com/q.B"}
			if mode == "unsupported" {
				want = []string{}
			}
			if got == nil || !slices.Equal(got, want) {
				t.Fatalf("scope: %v, want %v", got, want)
			}
			entries, _ := os.ReadDir(filepath.Join(chdir, ".stipulator/bindings"))
			if mode == "success" && len(entries) != 1 || mode != "success" && len(entries) != 0 {
				t.Fatalf("writes after %s: %v", mode, entries)
			}
		})
	}
}

func TestCLINamedPinScopeAndPrewriteFailures(t *testing.T) {
	for _, mode := range []string{"success", "setup failure", "resolution failure", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			write := scaffoldCorpus(t)
			write("docs/specs/a.md", "# A\n\n**REQ-aa-a** (behavior): It MUST a.\n\n**REQ-aa-b** (behavior): It MUST b.\n")
			stale := strings.Repeat("0", 64)
			binding := func(id, symbol string) string {
				return fmt.Sprintf("bindings { requirement_id: %q content_hash: %q backend: \"go\" symbol: %q role: BINDING_ROLE_IMPLEMENTS }\n", id, stale, symbol)
			}
			before := binding("REQ-aa-a", "example.com/p.A") + binding("REQ-aa-b", "example.com/bad.B")
			const path = ".stipulator/bindings/a.textproto"
			write(path, before)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			declarationBackends = func(_ context.Context, _ string, symbols []string) (map[string]verify.Backend, func() error, error) {
				if !slices.Equal(symbols, []string{"example.com/p.A"}) {
					t.Fatalf("scope %v", symbols)
				}
				if mode == "setup failure" {
					return nil, nil, fmt.Errorf("setup failed")
				}
				return map[string]verify.Backend{"go": authoringBackend(func(symbol string) (verify.Resolution, string, error) {
					data, err := os.ReadFile(filepath.Join(chdir, path))
					if err != nil || string(data) != before {
						t.Fatalf("CLI shapes read post-write state: %s %v", data, err)
					}
					if mode == "cancel" {
						cancel()
						return verify.NotFound, "", ctx.Err()
					}
					if mode == "resolution failure" {
						return verify.NotFound, "", fmt.Errorf("resolution failed")
					}
					return verify.Resolved, strings.Repeat("a", 64), nil
				})}, func() error { return nil }, nil
			}
			cmd := pinCmd()
			cmd.SetArgs([]string{"--req", "REQ-aa-a"})
			err := cmd.ExecuteContext(ctx)
			wantFailure := mode == "setup failure" || mode == "cancel"
			if (err != nil) != wantFailure {
				t.Fatalf("result: %v", err)
			}
			data, readErr := os.ReadFile(filepath.Join(chdir, path))
			if readErr != nil {
				t.Fatal(readErr)
			}
			if (string(data) == before) != wantFailure {
				t.Fatalf("write semantics changed: %s", data)
			}
			if !strings.Contains(string(data), stale) {
				t.Fatal("unselected pin changed")
			}
		})
	}
}
