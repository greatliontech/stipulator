// Package corpus locates and enumerates the specification corpus.
//
// The corpus is defined by the manifest — stipulator.textproto at the
// repository root — whose include globs select markdown documents. All paths
// are slash-separated and relative to the repository root. The package
// operates on an fs.FS so callers choose the tree source (working directory,
// git revision, test fixture): verification is a function of tree state and
// never reads VCS history.
package corpus

import (
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"google.golang.org/protobuf/encoding/prototext"
)

// ManifestPath is the manifest's fixed location relative to the repository
// root. A repository without a manifest is not a stipulator repository.
const ManifestPath = ".stipulator/manifest.textproto"

// DefaultInclude is the include glob in effect when the manifest declares
// none.
const DefaultInclude = "docs/specs/**/*.md"

// readmeBasename names a folder readme, which is navigation for humans and
// never part of the corpus, even when an include glob matches it.
const readmeBasename = "README.md"

// LoadManifest reads and parses the manifest from fsys, which must be rooted
// at the repository root. The returned manifest is normalized: when the file
// declares no include globs, DefaultInclude is applied.
func LoadManifest(fsys fs.FS) (*stipulatorv1.Manifest, error) {
	b, err := fs.ReadFile(fsys, ManifestPath)
	if err != nil {
		return nil, fmt.Errorf("reading manifest %s: %w", ManifestPath, err)
	}
	m := &stipulatorv1.Manifest{}
	if err := prototext.Unmarshal(b, m); err != nil {
		return nil, fmt.Errorf("parsing manifest %s: %w", ManifestPath, err)
	}
	if len(m.GetInclude()) == 0 {
		m.SetInclude([]string{DefaultInclude})
	}
	return m, nil
}

// Enumerate resolves the manifest's include globs against fsys and returns
// the corpus document paths, duplicate-free, sorted lexicographically, and
// never containing folder readmes. Malformed globs are rejected
// up front, before any matching, so a typo cannot silently shrink the
// corpus on trees that never reach the bad segment.
func Enumerate(fsys fs.FS, m *stipulatorv1.Manifest) ([]string, error) {
	globs := m.GetInclude()
	for _, g := range globs {
		if err := validateGlob(g); err != nil {
			return nil, fmt.Errorf("include glob %q: %w", g, err)
		}
	}
	var out []string
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if path.Base(p) == readmeBasename {
			return nil
		}
		for _, g := range globs {
			ok, err := matchGlob(g, p)
			if err != nil {
				return fmt.Errorf("include glob %q: %w", g, err)
			}
			if ok {
				out = append(out, p)
				return nil
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	// fs.WalkDir is lexical per directory, but emitting a subtree before
	// later siblings does not compose to global lexicographic order over
	// full paths ('/' sorts above '-' and '.'), so sort explicitly.
	slices.Sort(out)
	return out, nil
}

// validateGlob rejects a pattern containing an empty or malformed segment.
// path.Match validates a full segment pattern even when the name does not
// match, so probing with a fixed name surfaces ErrBadPattern eagerly.
func validateGlob(pattern string) error {
	for _, seg := range strings.Split(pattern, "/") {
		if seg == "" {
			return fmt.Errorf("empty segment: %w", path.ErrBadPattern)
		}
		if seg == "**" {
			continue
		}
		if _, err := path.Match(seg, "x"); err != nil {
			return fmt.Errorf("segment %q: %w", seg, err)
		}
	}
	return nil
}

// matchGlob reports whether the slash-separated path name matches pattern.
// Matching is per path segment: `**` as a complete pattern segment matches
// zero or more path segments; any other pattern segment follows
// path.Match syntax and matches exactly one path segment.
func matchGlob(pattern, name string) (bool, error) {
	return matchSegments(strings.Split(pattern, "/"), strings.Split(name, "/"))
}

func matchSegments(pat, segs []string) (bool, error) {
	for len(pat) > 0 {
		if pat[0] == "**" {
			// `**` consumes zero or more segments: try every split point.
			for i := 0; i <= len(segs); i++ {
				ok, err := matchSegments(pat[1:], segs[i:])
				if err != nil || ok {
					return ok, err
				}
			}
			return false, nil
		}
		if len(segs) == 0 {
			return false, nil
		}
		ok, err := path.Match(pat[0], segs[0])
		if err != nil || !ok {
			return ok, err
		}
		pat, segs = pat[1:], segs[1:]
	}
	return len(segs) == 0, nil
}
