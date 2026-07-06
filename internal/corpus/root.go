package corpus

import (
	"fmt"
	"os"
	"path/filepath"
)

// FindRoot locates the corpus root: the nearest ancestor of start (itself
// included) containing the manifest. Nearest wins, so corpora nest. This
// is command-surface ergonomics — the core always receives a tree already
// rooted here.
func FindRoot(start string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		if fi, err := os.Stat(filepath.Join(dir, filepath.FromSlash(ManifestPath))); err == nil && !fi.IsDir() {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("not inside a stipulator repository (no %s in %s or any parent); run `stipulator init` to scaffold one", ManifestPath, start)
		}
		dir = parent
	}
}
