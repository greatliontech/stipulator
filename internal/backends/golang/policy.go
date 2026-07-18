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
// payload semantics only this backend understands. Validation here is
// static — record-only, no toolchain work — so a refused record costs
// nothing; environment-effective checks (ambient GOFLAGS, the external
// package driver) live in NormalizeInvocation, which is where the
// environment first enters.
func (Policy) ValidateInvocation(invocation string, payload proto.Message) error {
	cfg, ok := payload.(*stipulatorv1.GoInvocationConfig)
	if !ok {
		return fmt.Errorf("the Go backend claims only GoInvocationConfig payloads, got %T", payload)
	}
	return validateConfig(cfg)
}

// validateConfig statically validates every typed field of one Go payload.
func validateConfig(cfg *stipulatorv1.GoInvocationConfig) error {
	if err := validateModuleRoot(cfg.GetModuleRoot()); err != nil {
		return err
	}
	for _, p := range cfg.GetPackages() {
		if err := validatePackagePattern(p); err != nil {
			return err
		}
	}
	if cfg.HasToolchain() {
		if err := validateBareToken("toolchain", cfg.GetToolchain()); err != nil {
			return err
		}
	}
	if err := validateEnvOverrides(cfg.GetEnvironment()); err != nil {
		return err
	}
	if err := validateEnvDeny(cfg.GetEnvDeny()); err != nil {
		return err
	}
	if cfg.HasGoos() {
		if err := validateBareToken("goos", cfg.GetGoos()); err != nil {
			return err
		}
	}
	if cfg.HasGoarch() {
		if err := validateBareToken("goarch", cfg.GetGoarch()); err != nil {
			return err
		}
	}
	for _, tag := range cfg.GetTags() {
		if err := validateBuildTag(tag); err != nil {
			return err
		}
	}
	if cfg.HasGoflags() {
		if err := validateGoflags(cfg.GetGoflags()); err != nil {
			return err
		}
	}
	switch cfg.GetWorkspaceMode() {
	case stipulatorv1.GoWorkspaceMode_GO_WORKSPACE_MODE_UNSPECIFIED,
		stipulatorv1.GoWorkspaceMode_GO_WORKSPACE_MODE_WORKSPACE,
		stipulatorv1.GoWorkspaceMode_GO_WORKSPACE_MODE_OFF:
	default:
		return fmt.Errorf("workspace_mode %d is not a recognized mode", cfg.GetWorkspaceMode())
	}
	switch cfg.GetModuleMode() {
	case stipulatorv1.GoModuleMode_GO_MODULE_MODE_UNSPECIFIED,
		stipulatorv1.GoModuleMode_GO_MODULE_MODE_READONLY,
		stipulatorv1.GoModuleMode_GO_MODULE_MODE_VENDOR,
		stipulatorv1.GoModuleMode_GO_MODULE_MODE_MOD:
	default:
		return fmt.Errorf("module_mode %d is not a recognized mode", cfg.GetModuleMode())
	}
	if cfg.HasPgo() {
		if err := validatePGO(cfg.GetPgo()); err != nil {
			return err
		}
	}
	if cfg.HasCount() && cfg.GetCount() <= 0 {
		return fmt.Errorf("count %d must be positive", cfg.GetCount())
	}
	switch cfg.GetCacheMode() {
	case stipulatorv1.GoCacheMode_GO_CACHE_MODE_UNSPECIFIED,
		stipulatorv1.GoCacheMode_GO_CACHE_MODE_ENABLED:
	case stipulatorv1.GoCacheMode_GO_CACHE_MODE_BYPASS:
		if cfg.HasCount() && cfg.GetCount() != 1 {
			return fmt.Errorf("cache_mode BYPASS is incompatible with count %d; bypass means count 1 semantics", cfg.GetCount())
		}
	default:
		return fmt.Errorf("cache_mode %d is not a recognized mode", cfg.GetCacheMode())
	}
	for _, a := range cfg.GetArgs() {
		if strings.ContainsRune(a, 0) {
			return fmt.Errorf("args entry %q contains NUL", a)
		}
		// The runtime-input capture file is per-process executor property:
		// a reviewed redirection would sever the capture from the process
		// the executor attributes it to, so the ingested file could carry
		// another writer's bytes — or nothing — as completed evidence.
		if name := strings.TrimLeft(a, "-"); len(name) != len(a) {
			if i := strings.IndexByte(name, '='); i >= 0 {
				name = name[:i]
			}
			if name == "test.testlogfile" {
				return fmt.Errorf("args entry %q names the runtime-input capture file, which the executor owns per process", a)
			}
			if selectionBinaryArgs[name] {
				return fmt.Errorf("args entry %q names the test selection, which the executor owns per process", a)
			}
		}
	}
	return nil
}

// validatePackagePattern refuses a pattern that could escape the tree or
// inject flags into the spawned go command. Import-path patterns and
// tree-relative "./..." forms pass; absolute paths, parent escapes,
// host-specific runes, and flag-shaped entries are refused.
func validatePackagePattern(p string) error {
	switch {
	case p == "":
		return fmt.Errorf("packages entry is empty")
	case strings.ContainsRune(p, 0):
		return fmt.Errorf("packages entry %q contains NUL", p)
	case strings.HasPrefix(p, "-"):
		return fmt.Errorf("packages entry %q is flag-shaped; patterns must not begin with '-'", p)
	case !hostPortableTreePath(p):
		return fmt.Errorf("packages entry %q carries host-specific path runes", p)
	case strings.HasPrefix(p, "/"):
		return fmt.Errorf("packages entry %q is absolute; patterns are module-relative or import paths", p)
	}
	if clean := path.Clean(p); clean == ".." || strings.HasPrefix(clean, "../") {
		return fmt.Errorf("packages entry %q escapes the verification tree", p)
	}
	return nil
}

// pinnedEnvKeys are the environment variables the backend itself owns —
// each is pinned from exactly one typed source (a config field or the
// workspace declaration), so carrying it through the generic environment
// fields would store one fact in two places.
var pinnedEnvKeys = map[string]bool{
	"GOWORK": true, "GOPACKAGESDRIVER": true, "GOOS": true, "GOARCH": true,
	"CGO_ENABLED": true, "GOFLAGS": true, "GOTOOLCHAIN": true,
}

func validateEnvOverrides(entries []string) error {
	seen := map[string]bool{}
	for _, e := range entries {
		if strings.ContainsRune(e, 0) {
			return fmt.Errorf("environment entry %q contains NUL", e)
		}
		eq := strings.IndexByte(e, '=')
		if eq <= 0 {
			return fmt.Errorf("environment entry %q is not KEY=VALUE with a non-empty key", e)
		}
		key := e[:eq]
		if pinnedEnvKeys[key] {
			return fmt.Errorf("environment entry %q sets backend-pinned key %s; use its typed field", e, key)
		}
		if seen[key] {
			return fmt.Errorf("environment sets duplicate key %q", key)
		}
		seen[key] = true
	}
	return nil
}

func validateEnvDeny(names []string) error {
	seen := map[string]bool{}
	for _, n := range names {
		if n == "" || strings.ContainsAny(n, "=\x00") {
			return fmt.Errorf("env_deny entry %q is not a bare variable name", n)
		}
		if pinnedEnvKeys[n] {
			return fmt.Errorf("env_deny entry %q names a backend-pinned key; use its typed field", n)
		}
		if seen[n] {
			return fmt.Errorf("env_deny names duplicate key %q", n)
		}
		seen[n] = true
	}
	return nil
}

// validateBareToken accepts a single shell-safe token: non-empty, no
// whitespace, no NUL, no '=' (which would smuggle an environment entry).
func validateBareToken(field, v string) error {
	if v == "" || strings.ContainsAny(v, " \t\n\x00=") {
		return fmt.Errorf("%s %q is not a bare token", field, v)
	}
	return nil
}

func validateBuildTag(tag string) error {
	if tag == "" || strings.HasPrefix(tag, "-") || strings.ContainsAny(tag, ", \t\n\x00") {
		return fmt.Errorf("tags entry %q is not a valid build tag", tag)
	}
	return nil
}

// ownedGoflags maps GOFLAGS-carried flags to the typed field that owns
// them: an ambient or explicit GOFLAGS carrying one would silently reshape
// the reviewed invocation beside its typed declaration.
var ownedGoflags = map[string]string{
	"race": "race", "tags": "tags", "mod": "module_mode", "pgo": "pgo",
	"count":   "count",
	"timeout": "the envelope timeout (per-binary bounds ride the reviewed args as -test.timeout)",
}

// unsupportedGoflags are the ambient controls refused outright — the
// overlay-refusal class gofresh's build-flag validation implements:
// source and tool substitution the verification model cannot represent.
var unsupportedGoflags = map[string]string{
	"overlay":  "freshness analysis hashes disk source",
	"toolexec": "an ambient tool substitution must never shape verification",
	"exec":     "an ambient test-binary substitution must never shape verification",
	"ldflags":  "ambient linker flags change compiled semantics outside the reviewed record",
	"gcflags":  "ambient compiler flags change compiled semantics outside the reviewed record",
	"asmflags": "ambient assembler flags change compiled semantics outside the reviewed record",
}

// selectionGoflags shape which obligations execute; carried ambiently they
// defeat conservation silently — a run selecting nothing partitions cleanly
// while executing nothing — so they are refused wherever GOFLAGS carries them.
var selectionGoflags = map[string]bool{
	"run": true, "skip": true, "short": true, "failfast": true,
	"list": true, "bench": true, "benchtime": true, "fuzz": true, "fuzztime": true,
}

// selectionBinaryArgs are the binary-side selection flags no reviewed
// args entry may carry — the -test.* twins of the selection controls
// GOFLAGS validation already refuses at the go-command level, plus the
// bare "run" form, inert binary-side but refused for symmetry with the
// family. Selection is executor and reviewed-record property exactly as
// the capture file is: the executor renders each process's selection at
// the go-command level (the whole package, or one isolated runnable),
// and the test binary honors the last value it parses — a reviewed entry
// would silently reshape which obligations execute beneath the
// executor's own rendering.
var selectionBinaryArgs = map[string]bool{
	"test.run": true, "test.skip": true, "test.list": true,
	"test.bench": true, "test.fuzz": true, "run": true,
}

// validateGoflags checks one GOFLAGS value, explicit or effective.
func validateGoflags(goflags string) error {
	for _, word := range strings.Fields(goflags) {
		word = strings.Trim(word, `"'`)
		name := strings.TrimLeft(word, "-")
		if i := strings.IndexByte(name, '='); i >= 0 {
			name = name[:i]
		}
		if reason, ok := unsupportedGoflags[name]; ok {
			return fmt.Errorf("GOFLAGS carries -%s, which is unsupported: %s", name, reason)
		}
		if selectionGoflags[name] {
			return fmt.Errorf("GOFLAGS carries -%s, which shapes test selection outside the reviewed record", name)
		}
		if owner, ok := ownedGoflags[name]; ok {
			return fmt.Errorf("GOFLAGS carries -%s, which is owned by %s; declare it there", name, owner)
		}
	}
	return nil
}

// validatePGO accepts the toolchain's keyword selections or a committed
// tree-relative profile path.
func validatePGO(v string) error {
	if v == "auto" || v == "off" {
		return nil
	}
	if err := validateModuleRoot(v); err != nil || v == "" {
		return fmt.Errorf("pgo %q is not \"auto\", \"off\", or a tree-relative slash path to a committed profile", v)
	}
	return nil
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
// envelope the migration review sees and can tighten.
const derivedTimeout = 2 * time.Hour

// derivedBinaryTimeout is the per-binary bound the derived record carries,
// mirroring the legacy suite's explicit thirty-minute test-binary timeout.
// The executor disables the toolchain's implicit per-binary default, so
// without this argument each binary would be bounded only by the
// invocation envelope; deriving the legacy bound keeps the record's
// semantics exactly what the legacy suite ran, and the migration review
// can tighten or drop it deliberately.
const derivedBinaryTimeout = "-test.timeout=30m"

// DerivePolicy derives the accepted-test-policy record equivalent to the
// universal race suite the legacy witness pipeline executes: one
// race-enabled `./...` invocation per workspace member — the go.work
// member enumeration RunTests iterates, the root module alone when the
// tree declares no workspace — each under the ceiling the legacy suite
// declares and carrying its explicit per-binary timeout. Module roots are
// recorded tree-relative in slash form and invocation names derive from
// them alone, never from host paths, so two hosts derive byte-identical
// records from the same workspace declaration.
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
		cfg.SetArgs([]string{derivedBinaryTimeout})
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
