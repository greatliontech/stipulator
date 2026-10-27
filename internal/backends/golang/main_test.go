package golang

import (
	"context"
	"flag"
	"os"
	"regexp"
	"testing"
)

// TestMain loads the shared repository-tree backend: after flags parse,
// before m.Run installs the testlog, so the tree read stays outside
// every witness's observation exactly as a package-init load kept it,
// and only on the full tier when at least one selected test reads it.
func TestMain(m *testing.M) {
	flag.Parse()
	if !testing.Short() && needsRepoBackend() {
		b, err := newContext(context.Background(), "../../..", nil)
		if err != nil {
			panic(err)
		}
		backend = b
	}
	os.Exit(m.Run())
}

func needsRepoBackend() bool {
	f := flag.Lookup("test.run")
	if f == nil {
		return true
	}
	return needsRepoBackendFor(f.Value.String())
}

func needsRepoBackendFor(top string) bool {
	if top == "" {
		return true
	}
	cs := 0
	cp := 0
	for i := 0; i < len(top); i++ {
		switch top[i] {
		case '[':
			cs++
		case ']':
			if cs--; cs < 0 {
				cs = 0
			}
		case '(':
			if cs == 0 {
				cp++
			}
		case ')':
			if cs == 0 {
				cp--
			}
		case '\\':
			i++
		case '/':
			if cs == 0 && cp == 0 {
				top = top[:i]
				goto split
			}
		}
	}
split:
	re, err := regexp.Compile(top)
	if err != nil {
		return true
	}
	for _, name := range [...]string{
		"TestWitnessClassProof",
		"TestResolve",
		"TestShapeHashDistinguishesSignatures",
		"TestWitnessClass",
		"TestSlice",
		"TestSymbolFile",
		"TestReachedPackages",
		"TestRandomSeeded",
	} {
		if re.MatchString(name) {
			return true
		}
	}
	return false
}

func TestNeedsRepoBackendFor(t *testing.T) {
	for _, tc := range []struct {
		pattern string
		want    bool
	}{
		{"", true},
		{"^TestDerive$", false},
		{"TestResolve/exported_func", true},
		{"TestOther/TestResolve", false},
		{"(TestOther/sub|TestSlice)", true},
		{"Test[(]|TestResolve/exported_func", true},
		{"Test[/]Other|TestSlice", true},
		{`Test[\]]|TestResolve/sub`, true},
		{"Test[[:alpha:](]|TestResolve/exported_func", true},
		{"Test[[:alpha:]/]esolve", true},
		{"Test[", true},
	} {
		if got := needsRepoBackendFor(tc.pattern); got != tc.want {
			t.Errorf("needsRepoBackendFor(%q) = %t, want %t", tc.pattern, got, tc.want)
		}
	}
}
