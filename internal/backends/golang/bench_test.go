package golang

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"
)

//gofresh:pure
func BenchmarkSymbolPackageCandidates(b *testing.B) {
	const symbol = "github.com/greatliontech/stipulator/internal/backends/golang.Served.ResolveIn"
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		candidates := symbolPackageCandidates(symbol)
		if len(candidates) == 0 {
			b.Fatal("expected candidates")
		}
	}
}

//gofresh:pure
func BenchmarkWarmGraphs(b *testing.B) {
	owners := make([]string, 16)
	for i := range owners {
		owners[i] = fmt.Sprintf("example.com/repo/internal/pkg%02d", i)
	}
	view := &selectedContext{key: "default"}
	loader := func(_ context.Context, _ *selectedContext, owner string, _ *sourceFiles) (string, error) {
		var rows []sourcePackageRow
		for i := range 12 {
			rows = append(rows, sourcePackageRow{
				ImportPath: fmt.Sprintf("%s/dep%02d", owner, i),
				Dir:        fmt.Sprintf("/workspace/internal/pkg%02d", i),
				Name:       fmt.Sprintf("dep%02d", i),
				GoFiles:    []string{"a.go", "b.go", "c.go"},
			})
		}
		files := map[string]string{}
		sum := sha256.Sum256([]byte(owner))
		for i := range 64 {
			sum = sha256.Sum256(sum[:])
			files[fmt.Sprintf("/workspace/%s/file%02d.go", owner, i)] = hex.EncodeToString(sum[:])
		}
		return sourceDigest(struct {
			Packages []sourcePackageRow
			Files    map[string]string
		}{rows, files})
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		ep := newSelectionEndpoint(context.Background(), "/workspace", []buildSelection{{}})
		ep.graphLoader = loader
		ep.warmGraphs(view, owners)
		if len(ep.graphs) != len(owners) {
			b.Fatalf("graphs = %d, want %d", len(ep.graphs), len(owners))
		}
	}
}
