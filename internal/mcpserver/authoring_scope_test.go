package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/greatliontech/stipulator/internal/verify"
)

type authoringBackend func(string) (verify.Resolution, string, error)

func (f authoringBackend) Resolve(s string) (verify.Resolution, string, error) { return f(s) }

func TestMCPExplicitBindCompleteScopeAndAtomicFailure(t *testing.T) {
	for _, mode := range []string{"success", "late failure", "cancel", "unsupported", "empty"} {
		t.Run(mode, func(t *testing.T) {
			var server *Server
			_, writes := harnessWith(t, nil, func(s *Server) { server = s })
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var got []string
			calls := 0
			server.declarations = func(_ context.Context, symbols []string) (map[string]verify.Backend, error) {
				got = symbols
				return map[string]verify.Backend{"go": authoringBackend(func(string) (verify.Resolution, string, error) {
					calls++
					if calls == 2 && mode == "late failure" {
						return verify.NotFound, "", fmt.Errorf("selected declaration failed")
					}
					if calls == 2 && mode == "cancel" {
						cancel()
					}
					return verify.Resolved, strings.Repeat("a", 64), nil
				})}, nil
			}
			in := bindIn{Claims: []bindClaim{{Requirement: "REQ-m-a", Symbol: "example.com/p.A", Role: "implements"}, {Requirement: "REQ-m-b", Symbol: "example.com/q.B", Role: "implements"}}}
			if mode == "unsupported" {
				in.Backend = "unknown"
			}
			if mode == "empty" {
				in = bindIn{}
			}
			_, _, err := server.toolBind(ctx, &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: "bind"}}, in)
			if mode == "success" && err != nil || mode != "success" && err == nil {
				t.Fatalf("result %v", err)
			}
			if mode == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation %v", err)
			}
			if mode != "empty" {
				want := []string{"example.com/p.A", "example.com/q.B"}
				if mode == "unsupported" {
					want = []string{}
				}
				if got == nil || !slices.Equal(got, want) {
					t.Fatalf("scope: %v, want %v", got, want)
				}
			}
			if mode == "success" && len(writes) != 1 || mode != "success" && len(writes) != 0 {
				t.Fatalf("writes: %v", writes)
			}
		})
	}
}

func TestMCPNamedPinScopeAndPostwriteFailures(t *testing.T) {
	for _, mode := range []string{"success", "setup failure", "resolution failure", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			stale := strings.Repeat("0", 64)
			binding := func(id, symbol string) string {
				return fmt.Sprintf("bindings { requirement_id: %q content_hash: %q backend: \"go\" symbol: %q role: BINDING_ROLE_IMPLEMENTS }\n", id, stale, symbol)
			}
			before := binding("REQ-m-a", "example.com/p.A") + binding("REQ-m-b", "example.com/bad.B")
			const path = ".stipulator/bindings/m.textproto"
			var server *Server
			_, writes := harnessWith(t, map[string]string{path: before}, func(s *Server) { server = s })
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			server.declarations = func(_ context.Context, symbols []string) (map[string]verify.Backend, error) {
				if !slices.Equal(symbols, []string{"example.com/p.A"}) {
					t.Fatalf("scope %v", symbols)
				}
				data, err := fs.ReadFile(server.fsys(), path)
				if err != nil || string(data) == before {
					t.Fatalf("MCP setup lost post-write ordering: %s %v", data, err)
				}
				if mode == "setup failure" {
					return nil, fmt.Errorf("setup failed")
				}
				return map[string]verify.Backend{"go": authoringBackend(func(string) (verify.Resolution, string, error) {
					if mode == "cancel" {
						cancel()
						return verify.NotFound, "", ctx.Err()
					}
					if mode == "resolution failure" {
						return verify.NotFound, "", fmt.Errorf("resolution failed")
					}
					return verify.Resolved, strings.Repeat("a", 64), nil
				})}, nil
			}
			_, out, err := server.toolPin(ctx, &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: "pin"}}, pinIn{Ids: "REQ-m-a"})
			wantFailure := mode == "setup failure" || mode == "cancel"
			if (err != nil) != wantFailure {
				t.Fatalf("result: %v", err)
			}
			if wantFailure && !strings.Contains(err.Error(), path) {
				t.Fatalf("failure hid landed writes: %v", err)
			}
			if mode == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation %v", err)
			}
			if len(writes) != 1 || string(writes[path]) == before {
				t.Fatalf("lost editorial writes: %v", writes)
			}
			if !strings.Contains(string(writes[path]), stale) {
				t.Fatal("unselected pin changed")
			}
			if mode == "resolution failure" && !strings.Contains(fmt.Sprint(out), "resolution failed") {
				t.Fatalf("resolution fault hidden: %v", out)
			}
		})
	}
}
