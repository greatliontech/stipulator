package golang

import (
	"context"
	"errors"
	"fmt"
	"go/types"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/greatliontech/go-x-tools/go/packages"
	"github.com/greatliontech/gofresh/gotool"
	"github.com/greatliontech/stipulator/internal/verify"
)

// missingExportFixture owns both the module and cache. Its dependency has no
// imports, so priming its export does not compile a disposable standard library.
func missingExportFixture(t *testing.T) (*packages.Config, string) {
	t.Helper()
	dir := t.TempDir()
	for name, text := range map[string]string{
		"go.mod":       "module missingexport.test\n\ngo 1.27.0\n",
		"dep/dep.go":   "package dep\nfunc Value() int { return 7 }\n",
		"bad/bad.go":   "package bad\nimport \"missingexport.test/dep\"\nfunc Value() int { return dep.Value() }\n",
		"good/good.go": "package good\nfunc Value() int { return 9 }\n",
	} {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	env := gotool.SetEnv(os.Environ(), "GOWORK", "off")
	env = gotool.SetEnv(env, "GOENV", "off")
	env = gotool.SetEnv(env, "GOFLAGS", "")
	env = gotool.SetEnv(env, "GOCACHE", t.TempDir())
	env = gotool.SetEnv(env, "GOPACKAGESDRIVER", "off")
	cfg := &packages.Config{Context: t.Context(), Dir: dir, Env: env,
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo | packages.NeedImports | packages.NeedExportFile}
	return cfg, primeMissingExport(t, cfg)
}

func primeMissingExport(t *testing.T, cfg *packages.Config) string {
	t.Helper()
	prime := *cfg
	prime.Mode = packages.NeedName | packages.NeedExportFile
	pkgs, err := packages.Load(&prime, "./dep")
	if err != nil || len(pkgs) != 1 || len(pkgs[0].Errors) != 0 || pkgs[0].ExportFile == "" {
		t.Fatalf("prime dependency: packages=%v error=%v", pkgs, err)
	}
	return pkgs[0].ExportFile
}

// The loader's synchronous command-completion log runs after go list has
// returned its metadata and before the typed import opens the export. Using
// that existing seam avoids a production hook or a racing cache remover.
func removeExportAfterList(t *testing.T, cfg *packages.Config, export string) *bool {
	t.Helper()
	var once sync.Once
	removed := false
	cfg.Logf = func(format string, args ...any) {
		if format != "%s for %v" || len(args) != 2 || !strings.Contains(fmt.Sprint(args[1]), " -export=true ") {
			return
		}
		once.Do(func() {
			if err := os.Remove(export); err != nil {
				t.Errorf("remove owned export: %v", err)
				return
			}
			removed = true
		})
	}
	return &removed
}

func TestMissingExportControlledTypedImport(t *testing.T) {
	cfg, export := missingExportFixture(t)
	removed := removeExportAfterList(t, cfg, export)
	pkgs, err := packages.Load(cfg, "./bad", "./good")
	if err != nil {
		t.Fatal(err)
	}
	if !*removed {
		t.Fatal("fixture did not remove its export between metadata and typed import")
	}
	if len(pkgs) != 2 {
		t.Fatalf("loaded %d roots, want both failed and unrelated owners", len(pkgs))
	}
	for _, pkg := range pkgs {
		if pkg.PkgPath == "missingexport.test/good" {
			if len(pkg.Errors) != 0 {
				t.Fatalf("unrelated package failed: %v", pkg.Errors)
			}
			continue
		}
		dep := pkg.Imports["missingexport.test/dep"]
		if dep == nil || dep.Types == nil {
			t.Fatalf("dependency metadata missing from %s", pkg.PkgPath)
		}
		if len(pkg.Errors) == 0 || len(dep.Errors) == 0 || dep.Types.Complete() || dep.ExportFile != export {
			t.Fatalf("missing real typed-import failure: owner errors=%v dependency errors=%v complete=%v export=%q want=%q", pkg.Errors, dep.Errors, dep.Types.Complete(), dep.ExportFile, export)
		}
		if !strings.Contains(pkg.Errors[0].Msg, export) {
			t.Fatalf("loader did not read missing export: %v", pkg.Errors)
		}
		t.Logf("controlled typed import: %v; dependency: %v", pkg.Errors, dep.Errors)
	}
}

func missingExportBackend(cfg *packages.Config, pkgs []*packages.Package, failures map[*packages.Package]*missingExportError) *Backend {
	sel := (buildSelection{}).key()
	typesPkg := map[*types.Package]*packages.Package{}
	byPath := map[string][]*packages.Package{}
	for _, pkg := range pkgs {
		if pkg.Types != nil {
			typesPkg[pkg.Types] = pkg
		}
		byPath[pkg.PkgPath] = append(byPath[pkg.PkgPath], pkg)
	}
	return &Backend{dir: cfg.Dir, pkgs: pkgs, exportFailures: failures,
		modules:       map[string]string{".": "missingexport.test"},
		loadSelection: map[int]string{0: sel},
		lazyCfg:       map[string]map[string]*packages.Config{".": {sel: cfg}},
		typesPkg:      typesPkg, byPath: byPath}
}

// selectedUndefinedDiagnostic reads the original loader observation, without
// using the walk's error formatter or verdict as its own expected answer.
func selectedUndefinedDiagnostic(t *testing.T, b *Backend, path, selection, declaration string) packages.Error {
	t.Helper()
	var found []packages.Error
	for _, pkg := range b.byPath[path] {
		if b.selectionOf(pkg) != selection || pkg.Types == nil || pkg.Types.Scope().Lookup(declaration) == nil {
			continue
		}
		if len(pkg.Errors) == 0 || !strings.Contains(pkg.Errors[0].Msg, "undefined: mystery") {
			t.Fatalf("%s in %q lacks its original undefined-symbol load error: %v", pkg.ID, selection, pkg.Errors)
		}
		found = append(found, pkg.Errors[0])
	}
	if len(found) != 1 {
		t.Fatalf("%s.%s in %q has %d matching loader observations, want one", path, declaration, selection, len(found))
	}
	return found[0]
}

func requireMissingExportRefusal(t *testing.T, err error, export string) *missingExportError {
	t.Helper()
	var refusal *missingExportError
	if !errors.As(err, &refusal) {
		t.Fatalf("want structured missing-export refusal, got %v", err)
	}
	for _, text := range []string{"Go export-cache unavailable", export, "missingexport.test/dep", "typed resolution refused", "go list -export -a", "same complete loader environment", "loader:", "could not import"} {
		if !strings.Contains(err.Error(), text) {
			t.Errorf("refusal lacks %q: %v", text, err)
		}
	}
	if len(refusal.missing) != 1 || refusal.missing[0] != (missingExport{"missingexport.test/dep", export}) {
		t.Fatalf("wrong structural observation: %+v", refusal.missing)
	}
	return refusal
}

func TestMissingExportRefusalPreservesSelectedView(t *testing.T) {
	for _, workspace := range []string{"off", "on"} {
		t.Run(workspace, func(t *testing.T) {
			cfg, _ := missingExportFixture(t)
			cfg.BuildFlags = []string{"-tags=cacheprobe", "-mod=readonly"}
			cfg.Env = gotool.SetEnv(cfg.Env, "GOFLAGS", "-trimpath")
			cfg.Env = gotool.SetEnv(cfg.Env, "GOTOOLCHAIN", "local")
			if workspace == "on" {
				work := filepath.Join(t.TempDir(), "go.work")
				if err := os.WriteFile(work, fmt.Appendf(nil, "go 1.27.0\nuse %s\n", cfg.Dir), 0o600); err != nil {
					t.Fatal(err)
				}
				cfg.Env = gotool.SetEnv(cfg.Env, "GOWORK", work)
			}
			// Read the cache from the selected GOENV file, not this process's
			// environment or an assumed default cache directory.
			cache := t.TempDir()
			goenv := filepath.Join(t.TempDir(), "goenv")
			if err := os.WriteFile(goenv, []byte("GOCACHE="+cache+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg.Env = gotool.SetEnv(cfg.Env, "GOENV", goenv)
			cfg.Env = gotool.SetEnv(cfg.Env, "GOCACHE", "")
			// Both the tag and explicit readonly module mode must survive.
			if err := os.WriteFile(filepath.Join(cfg.Dir, "bad/bad.go"), []byte("//go:build cacheprobe\n\npackage bad\nimport \"missingexport.test/dep\"\nfunc Value() int { return dep.Value() }\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			export := primeMissingExport(t, cfg)
			pkgs, failures, err := loadTypedPackages(cfg, "./bad", "./good")
			if err != nil || len(failures) != 0 {
				t.Fatalf("positive load: %v %v", err, failures)
			}
			prior := missingExportBackend(cfg, pkgs, failures)
			res, oldShape, err := prior.Resolve("missingexport.test/bad.Value")
			if err != nil || res != verify.Resolved {
				t.Fatalf("positive resolution: %v %v", res, err)
			}
			// A new view must refuse rather than retain this old signature.
			if err := os.WriteFile(filepath.Join(cfg.Dir, "bad/bad.go"), []byte("//go:build cacheprobe\n\npackage bad\nimport \"missingexport.test/dep\"\nfunc Value(x int) int { return dep.Value()+x }\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			removed := removeExportAfterList(t, cfg, export)
			pkgs, failures, err = loadTypedPackages(cfg, "./bad", "./good")
			if err != nil || !*removed {
				t.Fatalf("controlled load: %v removed=%v", err, *removed)
			}
			current := missingExportBackend(cfg, pkgs, failures)
			res, shape, err := current.Resolve("missingexport.test/bad.Value")
			refusal := requireMissingExportRefusal(t, err, export)
			if res != verify.NotFound || shape != "" {
				t.Fatalf("failed load served %v %q (old %q)", res, shape, oldShape)
			}
			if refusal.directory != cfg.Dir || refusal.cache != cache || refusal.goflags != "-trimpath" || refusal.toolchain != "local" || !slices.Equal(refusal.flags, cfg.BuildFlags) {
				t.Fatalf("lost selected context: %+v", refusal)
			}
			wantWork, _ := lookupEnv(cfg.Env, "GOWORK")
			if refusal.workspace != wantWork {
				t.Fatalf("workspace = %q, want %q", refusal.workspace, wantWork)
			}
			if res, _, err := current.Resolve("missingexport.test/good.Value"); err != nil || res != verify.Resolved {
				t.Fatalf("unrelated owner tainted: %v %v", res, err)
			}
			// Exercise the stated remedy under exactly the selected directory,
			// environment and flags; it regenerates exports rather than merely
			// rerunning a consumer that might keep earlier metadata.
			args := append([]string{"list", "-export", "-a"}, cfg.BuildFlags...)
			args = append(args, "missingexport.test/dep")
			cmd := exec.CommandContext(t.Context(), "go", args...)
			cmd.Dir, cmd.Env = cfg.Dir, cfg.Env
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("refresh: %v\n%s", err, out)
			}
			cfg.Logf = nil
			pkgs, failures, err = loadTypedPackages(cfg, "./bad")
			if err != nil || len(failures) != 0 {
				t.Fatalf("refreshed load: %v %v", err, failures)
			}
			res, shape, err = missingExportBackend(cfg, pkgs, failures).Resolve("missingexport.test/bad.Value")
			if err != nil || res != verify.Resolved || shape == oldShape {
				t.Fatalf("new source not resolved after refresh: %v %q %v", res, shape, err)
			}
		})
	}
}

func TestMissingExportRefusesLazyAndDeclaringSource(t *testing.T) {
	for _, path := range []string{"lazy", "declaring-source"} {
		t.Run(path, func(t *testing.T) {
			cfg, export := missingExportFixture(t)
			pkgs, failures, err := loadTypedPackages(cfg, "./bad")
			if err != nil || len(failures) != 0 {
				t.Fatalf("positive load: %v %v", err, failures)
			}
			fn := pkgs[0].Types.Scope().Lookup("Value").(*types.Func)
			b := missingExportBackend(cfg, nil, nil)
			removed := removeExportAfterList(t, cfg, export)
			if path == "lazy" {
				fd, pkg, err := b.funcDeclOf((buildSelection{}).key(), fn)
				requireMissingExportRefusal(t, err, export)
				if fd != nil || pkg != nil || len(b.declIndex) != 0 {
					t.Fatal("failed lazy load indexed a declaration")
				}
			} else {
				pkg, err := b.sourcePackage(cfg, "missingexport.test/bad")
				requireMissingExportRefusal(t, err, export)
				if pkg != nil {
					t.Fatal("failed declaring-source load returned a package")
				}
			}
			if !*removed {
				t.Fatal("export was not removed")
			}
		})
	}
}

func TestMissingExportDoesNotClassifySourceFailures(t *testing.T) {
	for _, fault := range []string{"syntax", "missing-source"} {
		t.Run(fault, func(t *testing.T) {
			cfg, export := missingExportFixture(t)
			if fault == "syntax" {
				if err := os.WriteFile(filepath.Join(cfg.Dir, "bad/bad.go"), []byte("package bad\nimport \"missingexport.test/dep\"\nfunc Value() int { return dep.Value() + }\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			} else {
				removeExportAfterList(t, cfg, filepath.Join(cfg.Dir, "bad/bad.go"))
			}
			pkgs, failures, err := loadTypedPackages(cfg, "./bad")
			if err != nil || len(pkgs) != 1 || len(pkgs[0].Errors) == 0 {
				t.Fatalf("source failure absent: %v %v", pkgs, err)
			}
			if len(failures) != 0 {
				t.Fatalf("source failure misclassified: %v", failures)
			}
			// A previously consumed, now absent export beside an unrelated
			// source error is not a failed export read.
			if err := os.Remove(export); err != nil {
				t.Fatal(err)
			}
			if missing := missingDependencyExports(pkgs[0]); len(missing) != 0 {
				t.Fatalf("unrelated missing export attributed: %v", missing)
			}
		})
	}
}

func TestMissingExportRequiresCacheAndFilesystemEvidence(t *testing.T) {
	cache := t.TempDir()
	export := filepath.Join(cache, "ab", "owned-d")
	dep := &packages.Package{PkgPath: "dependency.test", ExportFile: export,
		Types: types.NewPackage("dependency.test", "dep"), Errors: []packages.Error{{Kind: packages.UnknownError, Msg: "opaque loader diagnostic"}}}
	owner := &packages.Package{Errors: []packages.Error{{Kind: packages.TypeError, Msg: "opaque owner diagnostic"}}, Imports: map[string]*packages.Package{"dep": dep}}
	if missing := missingDependencyExports(owner); len(missing) != 1 {
		t.Fatalf("structural failure needs no diagnostic parser: %v", missing)
	}
	for _, root := range []string{"", "off", "relative", filepath.Join(cache, "other"), export} {
		if exportInCache(root, export) {
			t.Errorf("non-cache coordinate %q admitted", root)
		}
	}
	if !exportInCache(cache, export) {
		t.Fatal("selected cache export rejected")
	}
	if err := os.MkdirAll(filepath.Dir(export), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(export, []byte("unreadable is not absent"), 0); err != nil {
		t.Fatal(err)
	}
	if missing := missingDependencyExports(owner); len(missing) != 0 {
		t.Fatalf("existing export misclassified: %v", missing)
	}
	// A path descending through a regular file fails ENOTDIR, not ENOENT.
	dep.ExportFile = filepath.Join(export, "child")
	if missing := missingDependencyExports(owner); len(missing) != 0 {
		t.Fatalf("other filesystem error misclassified: %v", missing)
	}
}

func TestMissingExportDoesNotClassifyPermissionErrors(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "private")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	export := filepath.Join(dir, "export")
	if err := os.WriteFile(export, []byte("owned fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	if _, err := os.Stat(export); !errors.Is(err, os.ErrPermission) {
		t.Skipf("host does not enforce this fixture's directory permissions: %v", err)
	}
	dep := &packages.Package{PkgPath: "dependency.test", ExportFile: export,
		Types: types.NewPackage("dependency.test", "dep"), Errors: []packages.Error{{Msg: "opaque diagnostic"}}}
	owner := &packages.Package{Errors: []packages.Error{{Msg: "opaque diagnostic"}}, Imports: map[string]*packages.Package{"dep": dep}}
	if missing := missingDependencyExports(owner); len(missing) != 0 {
		t.Fatalf("permission error misclassified: %v", missing)
	}
}

func TestMissingExportPreservesCallerCancellation(t *testing.T) {
	cfg, export := missingExportFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	cfg.Context = ctx
	removed := removeExportAfterList(t, cfg, export)
	log := cfg.Logf
	cfg.Logf = func(format string, args ...any) {
		log(format, args...)
		if format == "%s for %v" && len(args) == 2 && strings.Contains(fmt.Sprint(args[1]), " -export=true ") {
			cancel()
		}
	}
	pkgs, failures, err := loadTypedPackages(cfg, "./bad")
	if !*removed || !errors.Is(err, context.Canceled) || pkgs != nil || failures != nil {
		t.Fatalf("cancelled load answered: removed=%v packages=%v failures=%v error=%v", *removed, pkgs, failures, err)
	}
}

func TestMissingExportEagerResolution(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture intercepts the Go launcher with a POSIX shell")
	}
	cfg, export := missingExportFixture(t)
	for _, key := range []string{"GOCACHE", "GOWORK", "GOENV", "GOFLAGS", "GOPACKAGESDRIVER"} {
		value, _ := lookupEnv(cfg.Env, key)
		t.Setenv(key, value)
	}
	realGo, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
	// The real go list writes real metadata. The launcher keeps its output
	// pipe open until it removes only this fixture's export, so packages.Load
	// cannot begin the typed import first. This exercises newContext's eager
	// wiring without adding a production injection point.
	script := "#!/bin/sh\n" + quote(realGo) + " \"$@\"\nstatus=$?\n" +
		"if [ \"$status\" -eq 0 ]; then\n case \" $* \" in\n *' -export=true '*) rm -- " + quote(export) + ";;\n esac\nfi\nexit \"$status\"\n"
	if err := os.WriteFile(filepath.Join(bin, "go"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	b, err := newContext(t.Context(), cfg.Dir, []string{"missingexport.test/bad", "missingexport.test/good"})
	if err != nil {
		t.Fatal(err)
	}
	res, shape, err := b.Resolve("missingexport.test/bad.Value")
	requireMissingExportRefusal(t, err, export)
	if res != verify.NotFound || shape != "" {
		t.Fatalf("failed eager view served %v %q", res, shape)
	}
	if res, _, err := b.Resolve("missingexport.test/good.Value"); err != nil || res != verify.Resolved {
		t.Fatalf("unrelated eager owner tainted: %v %v", res, err)
	}
}

func TestMissingExportStopsAtCompletedTransitiveDependency(t *testing.T) {
	cfg, export := missingExportFixture(t)
	for name, text := range map[string]string{
		"bridge/bridge.go":     "package bridge\nimport \"missingexport.test/dep\"\nfunc Value() int { return dep.Value() }\n",
		"ordinary/ordinary.go": "package ordinary\nimport \"missingexport.test/bridge\"\nfunc Value() int { return bridge.Value() + }\n",
	} {
		path := filepath.Join(cfg.Dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	removed := removeExportAfterList(t, cfg, export)
	pkgs, failures, err := loadTypedPackages(cfg, "./ordinary", "./bad", "./good")
	if err != nil || !*removed || len(pkgs) != 3 {
		t.Fatalf("controlled load: packages=%v removed=%v error=%v", pkgs, *removed, err)
	}
	byPath := map[string]*packages.Package{}
	for _, pkg := range pkgs {
		byPath[pkg.PkgPath] = pkg
	}
	ordinary, direct := byPath["missingexport.test/ordinary"], byPath["missingexport.test/bad"]
	bridge := ordinary.Imports["missingexport.test/bridge"]
	dep := direct.Imports["missingexport.test/dep"]
	if bridge == nil || bridge.Types == nil || !bridge.Types.Complete() || len(bridge.Errors) != 0 {
		t.Fatal("bridge must load complete export types despite its body's dependency failure")
	}
	if dep == nil || bridge.Imports["missingexport.test/dep"] != dep || len(dep.Errors) == 0 || dep.Types == nil || dep.Types.Complete() {
		t.Fatal("roots must share the actual failed transitive export dependency")
	}
	if !slices.ContainsFunc(ordinary.Errors, func(e packages.Error) bool { return e.Kind == packages.ParseError }) {
		t.Fatalf("ordinary root must have its unrelated syntax error: %v", ordinary.Errors)
	}
	b := missingExportBackend(cfg, pkgs, failures)
	res, shape, err := b.Resolve("missingexport.test/ordinary.Value")
	var refusal *missingExportError
	if err == nil || errors.As(err, &refusal) || res != verify.NotFound || shape != "" {
		t.Fatalf("syntax failure misattributed through completed bridge: %v %q %v", res, shape, err)
	}
	want := fmt.Sprintf("package %s has load errors: %v", ordinary.ID, ordinary.Errors[0])
	if err.Error() != want {
		t.Fatalf("ordinary diagnostic changed: got %q want %q", err, want)
	}
	_, _, err = b.Resolve("missingexport.test/bad.Value")
	requireMissingExportRefusal(t, err, export)
	if res, _, err := b.Resolve("missingexport.test/good.Value"); err != nil || res != verify.Resolved {
		t.Fatalf("unrelated owner tainted: %v %v", res, err)
	}
}

func TestMissingExportPhysicalCacheSelection(t *testing.T) {
	for _, mode := range []string{"escaping-shard", "cache-root-alias"} {
		t.Run(mode, func(t *testing.T) {
			cfg, _ := missingExportFixture(t)
			cfg.BuildFlags = []string{"-tags=cacheprobe", "-mod=readonly"}
			cfg.Env = gotool.SetEnv(cfg.Env, "GOFLAGS", "-trimpath")
			cache, _ := lookupEnv(cfg.Env, "GOCACHE")
			if mode == "cache-root-alias" {
				alias := filepath.Join(t.TempDir(), "cache")
				if err := os.Symlink(cache, alias); err != nil {
					t.Fatal(err)
				}
				cache = alias
				cfg.Env = gotool.SetEnv(cfg.Env, "GOCACHE", cache)
			}
			export := primeMissingExport(t, cfg)
			if mode == "escaping-shard" {
				shard := filepath.Dir(export)
				outside := filepath.Join(t.TempDir(), "shard")
				if err := os.Rename(shard, outside); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, shard); err != nil {
					t.Fatal(err)
				}
			}
			pkgs, failures, err := loadTypedPackages(cfg, "./bad", "./good")
			if err != nil || len(failures) != 0 || len(pkgs) != 2 {
				t.Fatalf("positive load: %v %v", err, failures)
			}
			for _, pkg := range pkgs {
				if len(pkg.Errors) != 0 {
					t.Fatalf("Go must accept this cache layout before removal: %v", pkg.Errors)
				}
			}
			removed := removeExportAfterList(t, cfg, export)
			pkgs, failures, err = loadTypedPackages(cfg, "./bad", "./good")
			if err != nil || !*removed {
				t.Fatalf("controlled load: %v removed=%v", err, *removed)
			}
			b := missingExportBackend(cfg, pkgs, failures)
			res, shape, err := b.Resolve("missingexport.test/bad.Value")
			if res != verify.NotFound || shape != "" || err == nil {
				t.Fatalf("failed import answered %v %q %v", res, shape, err)
			}
			if mode == "cache-root-alias" {
				refusal := requireMissingExportRefusal(t, err, export)
				if refusal.cache != cache || refusal.directory != cfg.Dir || refusal.goflags != "-trimpath" || refusal.workspace != "off" || !slices.Equal(refusal.flags, cfg.BuildFlags) {
					t.Fatalf("physical resolution changed selected context: %+v", refusal)
				}
			} else {
				var refusal *missingExportError
				if len(failures) != 0 || errors.As(err, &refusal) {
					t.Fatalf("outside-cache export misclassified: %v", err)
				}
				for _, pkg := range pkgs {
					if pkg.PkgPath == "missingexport.test/bad" {
						want := fmt.Sprintf("package %s has load errors: %v", pkg.ID, pkg.Errors[0])
						if err.Error() != want {
							t.Fatalf("loader diagnostic changed: got %q want %q", err, want)
						}
					}
				}
			}
			if res, _, err := b.Resolve("missingexport.test/good.Value"); err != nil || res != verify.Resolved {
				t.Fatalf("unrelated owner tainted: %v %v", res, err)
			}
		})
	}
}

func TestMissingExportPhysicalAncestors(t *testing.T) {
	cache := t.TempDir()
	if !exportInCache(cache, filepath.Join(cache, "absent-shard", "export")) {
		t.Fatal("missing suffix beneath an established cache root refused")
	}
	link := filepath.Join(cache, "ab")
	outside := t.TempDir()
	if err := os.Symlink(filepath.Join(outside, "absent"), link); err != nil {
		t.Fatal(err)
	}
	if exportInCache(cache, filepath.Join(link, "export")) {
		t.Fatal("unresolvable existing shard link was skipped")
	}
	if err := os.Mkdir(filepath.Join(outside, "absent"), 0o700); err != nil {
		t.Fatal(err)
	}
	if exportInCache(cache, link+string(os.PathSeparator)+".."+string(os.PathSeparator)+"export") {
		t.Fatal("lexical cleaning hid an escaping link before ..")
	}
	if exportInCache(filepath.Join(cache, "absent-root"), filepath.Join(cache, "absent-root", "export")) {
		t.Fatal("unestablished physical cache root admitted")
	}
}

func TestMissingExportHelperWalkRefusesPartialPackages(t *testing.T) {
	for _, scope := range []string{"eager", "lazy"} {
		for _, location := range []string{"cache", "escaping-shard"} {
			t.Run(scope+"/"+location, func(t *testing.T) {
				cfg, export := missingExportFixture(t)
				for name, text := range map[string]string{
					"bad/bad.go":       "package bad\nimport \"missingexport.test/dep\"\nfunc Helper() {}\nfunc Other() int { return dep.Value() }\n",
					"good/good.go":     "package good\nfunc Helper() {}\n",
					"caller/caller.go": "package caller\nimport (\"missingexport.test/bad\"; \"missingexport.test/good\")\nfunc Test() { bad.Helper() }\nfunc Good() { good.Helper() }\n",
				} {
					path := filepath.Join(cfg.Dir, name)
					if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				if location == "escaping-shard" {
					shard := filepath.Dir(export)
					outside := filepath.Join(t.TempDir(), "shard")
					if err := os.Rename(shard, outside); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(outside, shard); err != nil {
						t.Fatal(err)
					}
				}
				var removed *bool
				patterns := []string{"./caller", "./good"}
				if scope == "eager" {
					patterns = append(patterns, "./bad")
					removed = removeExportAfterList(t, cfg, export)
				}
				pkgs, failures, err := loadTypedPackages(cfg, patterns...)
				if err != nil {
					t.Fatal(err)
				}
				b := missingExportBackend(cfg, pkgs, failures)
				fd, caller, err := b.funcDecl("missingexport.test/caller.Test")
				if err != nil || len(caller.Errors) != 0 {
					t.Fatalf("caller itself must resolve: %v", err)
				}
				root := caller.Types.Scope().Lookup("Test").(*types.Func)
				helper := caller.Imports["missingexport.test/bad"].Types.Scope().Lookup("Helper").(*types.Func)
				if scope == "lazy" {
					removed = removeExportAfterList(t, cfg, export)
				} else {
					owner := b.typesPkg[helper.Pkg()]
					if owner == nil || len(owner.Errors) == 0 || len(owner.GoFiles) == 0 || helper == nil {
						t.Fatal("eager fixture must retain a callable in a failed source package")
					}
				}
				sel := (buildSelection{}).key()
				path := b.seededThrough(sel, root, fd, caller)
				if !*removed || path.refusal == "" || path.via != "" {
					t.Fatalf("helper walk consumed partial source: removed=%v path=%+v", *removed, path)
				}
				// Ask the declaration consumer too: a refusal must retain the
				// actual load error, not become a missing-helper answer merely
				// because the failed package was excluded from the index.
				b.walkMu.Lock()
				hfd, hpkg, loadErr := b.funcDeclOf(sel, helper)
				_, indexed := b.declIndex[walkKey(sel, helper)]
				_, consumed := b.bodies[walkKey(sel, helper)]
				b.walkMu.Unlock()
				if hfd != nil || hpkg != nil || loadErr == nil || indexed || consumed {
					t.Fatalf("failed helper admitted: decl=%v package=%v error=%v indexed=%v consumed=%v", hfd, hpkg, loadErr, indexed, consumed)
				}
				if location == "cache" {
					requireMissingExportRefusal(t, loadErr, export)
				} else {
					var refusal *missingExportError
					if errors.As(loadErr, &refusal) || !strings.Contains(loadErr.Error(), export) || !strings.Contains(loadErr.Error(), "could not import") {
						t.Fatalf("unclassified helper failure lost original loader diagnostic: %v", loadErr)
					}
				}
				if !strings.Contains(path.refusal, loadErr.Error()) {
					t.Fatalf("walk lost helper load disposition: %q; want %v", path.refusal, loadErr)
				}
				// Repeated walks remain refused; no partially typed declaration
				// was admitted to either the declaration or body memo.
				if again := b.seededThrough(sel, root, fd, caller); again.refusal != path.refusal {
					t.Fatalf("repeat changed load disposition: %+v", again)
				}
				goodFD, goodPkg, err := b.funcDecl("missingexport.test/caller.Good")
				if err != nil {
					t.Fatal(err)
				}
				good := goodPkg.Types.Scope().Lookup("Good").(*types.Func)
				if path := b.seededThrough(sel, good, goodFD, goodPkg); path.refusal != "" || path.via != "" {
					t.Fatalf("unrelated good helper tainted: %+v", path)
				}
			})
		}
	}
}
