package golang

import "testing"

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
