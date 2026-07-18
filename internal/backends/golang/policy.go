package golang

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"golang.org/x/mod/modfile"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
)

// Policy is the Go backend's policy surface: the dispatch target for the
// `go` payload case. It is deliberately stateless and loads no packages —
// payload validation precedes any toolchain work, so a refused record
// costs nothing.
type Policy struct{}

// ValidateInvocation implements the core policy dispatch seam for the Go
// payload: it claims exactly its own typed configuration and enforces the
// payload semantics only this backend understands.
func (Policy) ValidateInvocation(invocation string, payload proto.Message) error {
	cfg, ok := payload.(*stipulatorv1.GoInvocationConfig)
	if !ok {
		return fmt.Errorf("the Go backend claims only GoInvocationConfig payloads, got %T", payload)
	}
	return validateModuleRoot(cfg.GetModuleRoot())
}

// validateModuleRoot enforces the payload's hermeticity: a module root is
// slash-separated, tree-relative, already in clean form (empty means the
// repository root), and inside the tree. An escaping root would make the
// same committed record verify different trees per machine — refused
// exactly as an escaping go.work member is (REQ-go-workspace). Canonical
// form is refused, never repaired: the record is reviewed contract.
// hostPortableTreePath refuses runes that change path semantics between
// hosts: a backslash or drive colon validates as an ordinary rune under slash
// semantics here yet resolves as a separator or volume on Windows, escaping
// the tree exactly where the committed record must stay hermetic.
func hostPortableTreePath(p string) bool {
	return !strings.ContainsAny(p, "\\:")
}

func validateModuleRoot(root string) error {
	if root == "" {
		return nil
	}
	// The escape check runs over the cleaned path so a dressed-up escape
	// ("a/../../b") is named for what it is, not for its formatting.
	clean := path.Clean(root)
	switch {
	case !hostPortableTreePath(root):
		return fmt.Errorf("module_root %q carries host-specific path runes; module roots use forward slashes only", root)
	case strings.HasPrefix(root, "/"):
		return fmt.Errorf("module_root %q is absolute; module roots are tree-relative", root)
	case clean == ".." || strings.HasPrefix(clean, "../"):
		return fmt.Errorf("module_root %q escapes the verification tree; module roots must lie within it", root)
	case root == "." || clean != root:
		return fmt.Errorf("module_root %q is not in canonical slash form (empty means the repository root)", root)
	}
	return nil
}

// derivedTimeout is the per-invocation ceiling the derived record admits.
// The legacy suite bounded each test binary at thirty minutes with no
// invocation-level ceiling, so a member running many binaries was admitted
// far beyond thirty minutes serially; two hours is a generous explicit
// envelope the migration review sees and can tighten, while per-binary
// bounds return as typed Go configuration when those fields land.
const derivedTimeout = 2 * time.Hour

// DerivePolicy derives the accepted-test-policy record equivalent to the
// universal race suite the legacy witness pipeline executes: one
// race-enabled `./...` invocation per workspace member — the go.work
// member enumeration RunTests iterates, the root module alone when the
// tree declares no workspace — each under the ceiling the legacy suite
// declares. Module roots are recorded tree-relative in slash form and
// invocation names derive from them alone, never from host paths, so two
// hosts derive byte-identical records from the same workspace declaration.
func DerivePolicy(dir string) (*stipulatorv1.TestPolicy, error) {
	members, err := policyMembers(dir)
	if err != nil {
		return nil, err
	}
	// Names derive from module roots under one fixed prefix, so sorting
	// members sorts names: the record is born in canonical ascending
	// order rather than repaired into it.
	sort.Strings(members)
	invs := make([]*stipulatorv1.PolicyInvocation, 0, len(members))
	for _, m := range members {
		cfg := &stipulatorv1.GoInvocationConfig{}
		name := "race"
		if m != "" {
			cfg.SetModuleRoot(m)
			name = "race:" + m
		}
		cfg.SetPackages([]string{"./..."})
		cfg.SetRace(true)
		inv := &stipulatorv1.PolicyInvocation{}
		inv.SetName(name)
		inv.SetTimeout(durationpb.New(derivedTimeout))
		inv.SetGo(cfg)
		invs = append(invs, inv)
	}
	p := &stipulatorv1.TestPolicy{}
	p.SetInvocations(invs)
	return p, nil
}

// policyMembers enumerates the tree's Go module roots for policy
// derivation, tree-relative in slash form with "" for the repository
// root. It parallels workspaceMembers but stays in slash-path semantics
// throughout: the result is written into a committed record that must not
// vary by host separator.
func policyMembers(dir string) ([]string, error) {
	b, err := os.ReadFile(filepath.Join(dir, "go.work"))
	if errors.Is(err, fs.ErrNotExist) {
		return []string{""}, nil
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
		clean := path.Clean(u.Path)
		if !hostPortableTreePath(u.Path) || strings.HasPrefix(clean, "/") || clean == ".." || strings.HasPrefix(clean, "../") {
			// A member outside the tree would make the same committed
			// policy verify differently per machine: hermeticity is
			// refused away, never silently bent (REQ-go-workspace).
			return nil, fmt.Errorf("go.work member %q escapes the verification tree; members must lie within it", u.Path)
		}
		if clean == "." {
			clean = ""
		}
		members = append(members, clean)
	}
	if len(members) == 0 {
		return nil, fmt.Errorf("go.work declares no members")
	}
	return members, nil
}
