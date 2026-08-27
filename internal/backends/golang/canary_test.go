package golang

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/greatliontech/gofresh/shapecorpus"
)

// TestLanguageShapeCanaries runs the fleet's shared shape corpus
// (gofresh/shapecorpus) through this backend's symbol resolution and
// content hashing: each entry resolves its SHAPE-CARRYING symbol —
// the generic method, the alias-signed function, the origin — so the
// hash path itself sees type parameters and aliases, not only the
// loader. Runs under the CI matrix's next-rc leg like every test.
func TestLanguageShapeCanaries(t *testing.T) {
	for _, entry := range shapecorpus.Entries() {
		t.Run(entry.Name, func(t *testing.T) {
			dir := t.TempDir()
			for file, content := range entry.TestFiles() {
				if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			b, err := newContext(context.Background(), dir)
			if err != nil {
				t.Errorf("canary load: %v", err)
				return
			}
			for _, symbol := range []string{"example.com/shape.Subject", "example.com/shape." + entry.ShapeSymbol} {
				res, hash, err := b.Resolve(symbol)
				if err != nil {
					t.Errorf("canary resolve %s: %v", symbol, err)
					continue
				}
				if hash == "" {
					t.Errorf("canary %s resolved without a content hash: %v", symbol, res)
				}
			}
		})
	}
}
