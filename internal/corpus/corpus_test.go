package corpus

import (
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/stipulate"
)

func manifest(includes ...string) *stipulatorv1.Manifest {
	m := &stipulatorv1.Manifest{}
	m.SetInclude(includes)
	return m
}

func TestLoadManifest(t *testing.T) {
	stipulate.Covers(t, "REQ-profile-manifest")
	t.Run("missing manifest is an error", func(t *testing.T) {
		_, err := LoadManifest(fstest.MapFS{})
		if err == nil {
			t.Fatal("want error for absent manifest, got nil")
		}
	})

	t.Run("malformed textproto is an error", func(t *testing.T) {
		fsys := fstest.MapFS{
			ManifestPath: {Data: []byte(`include: [`)},
		}
		if _, err := LoadManifest(fsys); err == nil {
			t.Fatal("want parse error, got nil")
		}
	})

	t.Run("declared includes are returned verbatim", func(t *testing.T) {
		fsys := fstest.MapFS{
			ManifestPath: {Data: []byte("include: \"specs/**/*.md\"\ninclude: \"rfc/*.md\"\n")},
		}
		m, err := LoadManifest(fsys)
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"specs/**/*.md", "rfc/*.md"}
		if !slices.Equal(m.GetInclude(), want) {
			t.Fatalf("include = %v, want %v", m.GetInclude(), want)
		}
	})

	t.Run("empty include defaults", func(t *testing.T) {
		fsys := fstest.MapFS{
			ManifestPath: {Data: []byte("")},
		}
		m, err := LoadManifest(fsys)
		if err != nil {
			t.Fatal(err)
		}
		want := []string{DefaultInclude}
		if !slices.Equal(m.GetInclude(), want) {
			t.Fatalf("include = %v, want %v", m.GetInclude(), want)
		}
	})
}

func TestEnumerate(t *testing.T) {
	stipulate.Covers(t, "REQ-profile-enumeration")
	md := &fstest.MapFile{Data: []byte("x")}
	fsys := fstest.MapFS{
		"docs/specs/overview.md":          md,
		"docs/specs/profile.md":           md,
		"docs/specs/README.md":            md,
		"docs/specs/backends/go.md":       md,
		"docs/specs/backends/README.md":   md,
		"docs/specs/deep/a/b/c.md":        md,
		"docs/specs/notes.txt":            md,
		"docs/plans/stipulator.md":        md,
		"README.md":                       md,
		"internal/corpus/corpus.go":       md,
	}

	t.Run("doublestar matches zero and many segments, indexes excluded", func(t *testing.T) {
		got, err := Enumerate(fsys, manifest("docs/specs/**/*.md"))
		if err != nil {
			t.Fatal(err)
		}
		want := []string{
			"docs/specs/backends/go.md",
			"docs/specs/deep/a/b/c.md",
			"docs/specs/overview.md",
			"docs/specs/profile.md",
		}
		if !slices.Equal(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
	})

	t.Run("index exclusion beats an explicit glob", func(t *testing.T) {
		got, err := Enumerate(fsys, manifest("docs/specs/README.md"))
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 0 {
			t.Fatalf("got %v, want empty", got)
		}
	})

	t.Run("overlapping globs do not duplicate", func(t *testing.T) {
		got, err := Enumerate(fsys, manifest("docs/specs/**/*.md", "docs/**/*.md"))
		if err != nil {
			t.Fatal(err)
		}
		compacted := slices.Compact(slices.Clone(got))
		if len(compacted) != len(got) {
			t.Fatalf("duplicates in %v", got)
		}
		if !slices.Contains(got, "docs/plans/stipulator.md") {
			t.Fatalf("second glob not applied: %v", got)
		}
	})

	t.Run("single star does not cross segments", func(t *testing.T) {
		got, err := Enumerate(fsys, manifest("docs/specs/*.md"))
		if err != nil {
			t.Fatal(err)
		}
		want := []string{
			"docs/specs/overview.md",
			"docs/specs/profile.md",
		}
		if !slices.Equal(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
	})

	t.Run("output is sorted lexically", func(t *testing.T) {
		// Sibling names straddling '/' byte order: WalkDir emits the
		// a/ subtree before a-b/ and a.md, which is not sorted order.
		tricky := fstest.MapFS{
			"docs/specs/a/b.md":   md,
			"docs/specs/a-b/x.md": md,
			"docs/specs/a.md":     md,
		}
		got, err := Enumerate(tricky, manifest("docs/specs/**/*.md"))
		if err != nil {
			t.Fatal(err)
		}
		want := []string{
			"docs/specs/a-b/x.md",
			"docs/specs/a.md",
			"docs/specs/a/b.md",
		}
		if !slices.Equal(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
	})

	t.Run("bad pattern surfaces as error", func(t *testing.T) {
		_, err := Enumerate(fsys, manifest("docs/[/*.md"))
		if err == nil {
			t.Fatal("want ErrBadPattern-derived error, got nil")
		}
		if !strings.Contains(err.Error(), "docs/[/*.md") {
			t.Fatalf("error does not name the glob: %v", err)
		}
	})

	t.Run("bad pattern errors even when no path reaches it", func(t *testing.T) {
		_, err := Enumerate(fsys, manifest("nosuchdir/[/*.md"))
		if err == nil {
			t.Fatal("want eager validation error, got nil")
		}
	})

	t.Run("empty segment is rejected", func(t *testing.T) {
		for _, g := range []string{"/docs/**/*.md", "docs//x.md", "docs/specs/"} {
			if _, err := Enumerate(fsys, manifest(g)); err == nil {
				t.Errorf("glob %q: want error, got nil", g)
			}
		}
	})
}

func TestMatchGlob(t *testing.T) {
	cases := []struct {
		pattern, name string
		want          bool
	}{
		{"docs/specs/**/*.md", "docs/specs/a.md", true},
		{"docs/specs/**/*.md", "docs/specs/x/y/z.md", true},
		{"docs/specs/**/*.md", "docs/specs/a.txt", false},
		{"docs/specs/**/*.md", "docs/a.md", false},
		{"**", "anything/at/all", true},
		{"**/*.md", "top.md", true},
		{"*.md", "a/b.md", false},
		{"a/*/c.md", "a/b/c.md", true},
		{"a/*/c.md", "a/b/b2/c.md", false},
		{"a/**/c/**/e.md", "a/b/c/d/e.md", true},
		{"a/**/c/**/e.md", "a/c/e.md", true},
		{"a/**/c/**/e.md", "a/b/d/e.md", false},
	}
	for _, c := range cases {
		got, err := matchGlob(c.pattern, c.name)
		if err != nil {
			t.Fatalf("matchGlob(%q, %q): %v", c.pattern, c.name, err)
		}
		if got != c.want {
			t.Errorf("matchGlob(%q, %q) = %v, want %v", c.pattern, c.name, got, c.want)
		}
	}
}
