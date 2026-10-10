package mcpserver

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"strings"
	"testing"

	"github.com/greatliontech/stipulator/internal/prunetest"
	"github.com/greatliontech/stipulator/stipulate"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestPruneStoreErrorCarriesCompletedAccountOnTheWire(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-store-gc")
	for _, resolution := range []bool{false, true} {
		name := "witness"
		if resolution {
			name = "resolution"
		}
		t.Run(name, func(t *testing.T) {
			f := prunetest.New(t, resolution)
			s := &Server{root: f.Root, fsys: func() fs.FS { return os.DirFS(f.Root) }, capture: f.Capture}
			server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "v0"}, nil)
			returned := make(chan error, 1)
			// The transport remains live. The operation's own context cancels
			// at a deterministic deletion, so the SDK must deliver an error
			// result even though it discards the handler's typed output.
			mcp.AddTool(server, &mcp.Tool{Name: "prune", Description: "prune the test store"}, func(ctx context.Context, req *mcp.CallToolRequest, in pruneIn) (*mcp.CallToolResult, map[string]any, error) {
				ctx, cancel := f.Context(ctx)
				defer cancel()
				result, out, err := s.toolPrune(ctx, req, in)
				returned <- err
				return result, out, err
			})
			ct, st := mcp.NewInMemoryTransports()
			go func() { _ = server.Run(t.Context(), st) }()
			client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v0"}, nil)
			session, err := client.Connect(t.Context(), ct, nil)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { session.Close() })
			result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "prune", Arguments: map[string]any{"store": true}})
			if err != nil || result == nil || !result.IsError {
				t.Fatalf("wire reported success or no result: %+v %v", result, err)
			}
			if err := <-returned; !errors.Is(err, context.Canceled) {
				t.Fatalf("handler lost cancellation: %v", err)
			}
			want := "store gc: completed prefix; unexamined records are not counted as kept\nstore gc: 1 record variant(s) removed, 2 kept\ncontext canceled"
			if resolution {
				want = "store gc: completed prefix; unexamined records are not counted as kept\nstore gc: 3 record variant(s) removed, 0 kept\nstore gc: 1 resolution record(s) removed, 2 kept\ncontext canceled"
			}
			if text := toolText(t, result); !strings.Contains(text, want) {
				t.Fatalf("wire account=%q; want %q", text, want)
			}
			f.CheckPrefix(t)
		})
	}
}
