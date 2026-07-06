package golang

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/mod/modfile"
)

// workspaceMembers returns the tree's Go module directories, relative to
// dir: the go.work members when a workspace file is present, the root
// alone otherwise. Package patterns are module-scoped even in workspace
// mode, so every surface that walks "./..." — loading, witnessing — must
// iterate the members itself or nested modules silently vanish from
// verification.
func workspaceMembers(dir string) ([]string, error) {
	b, err := os.ReadFile(filepath.Join(dir, "go.work"))
	if errors.Is(err, fs.ErrNotExist) {
		return []string{"."}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading go.work: %w", err)
	}
	wf, err := modfile.ParseWork("go.work", b, nil)
	if err != nil {
		return nil, fmt.Errorf("parsing go.work: %w", err)
	}
	var members []string
	for _, u := range wf.Use {
		clean := filepath.Clean(u.Path)
		if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			// A member outside the tree would make the same commit verify
			// differently per machine: hermeticity is refused away, never
			// silently bent.
			return nil, fmt.Errorf("go.work member %q escapes the verification tree; members must lie within it", u.Path)
		}
		members = append(members, clean)
	}
	if len(members) == 0 {
		return nil, fmt.Errorf("go.work declares no members")
	}
	return members, nil
}

// goworkEnv pins workspace mode for a spawned go command or package load:
// the tree's own go.work when it has one, explicitly off otherwise. The
// go command discovers workspace files by walking UP, so an enclosing
// repository's workspace would otherwise leak into fixture or corpus
// trees that are not its members and refuse their "./..." patterns.
func goworkEnv(dir string) []string {
	work := filepath.Join(dir, "go.work")
	if _, err := os.Stat(work); err == nil {
		if abs, aerr := filepath.Abs(work); aerr == nil {
			work = abs
		}
		return append(os.Environ(), "GOWORK="+work)
	}
	return append(os.Environ(), "GOWORK=off")
}
