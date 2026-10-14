package golang

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"

	"github.com/greatliontech/gofresh/closure"
	"github.com/greatliontech/gofresh/gotool"
	"github.com/greatliontech/stipulator/internal/resolutioncache"
)

type certificateView struct {
	Selection                                      string
	Candidates                                     map[string]bool
	Environment, ToolchainContent, AdmissionPolicy string
	ContextFiles                                   map[string]string
	Graph                                          string
}

type sourceFiles struct {
	ctx     context.Context
	digests map[string]string
}

// beforeCertificateCloseForTest places a source edit after the native
// serving checks and before their final certificate check.
var beforeCertificateCloseForTest func()

// The executable content identifies the linked classifier, importer and
// frontend together, including local replacements without module checksums.
// Sample at process initialization, before a later installation can replace
// the executable's pathname. Linux exposes the running image independently
// of that pathname. Other hosts use the executable supplied at startup.
var sourceClassifierIdentity, sourceClassifierError = classifierIdentity()

func classifierIdentity() (string, error) {
	path, err := os.Executable()
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "linux" {
		path = "/proc/self/exe"
	}
	files := sourceFiles{ctx: context.Background(), digests: map[string]string{}}
	return files.digest(path)
}

func (f *sourceFiles) digest(path string) (string, error) {
	if err := f.ctx.Err(); err != nil {
		return "", err
	}
	path = filepath.Clean(path)
	if value, ok := f.digests[path]; ok {
		return value, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	h := sha256.New()
	buf := make([]byte, 32*1024)
	for {
		if err := f.ctx.Err(); err != nil {
			return "", err
		}
		n, err := file.Read(buf)
		if n > 0 {
			_, _ = h.Write(buf[:n])
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
	}
	value := hex.EncodeToString(h.Sum(nil))
	f.digests[path] = value
	return value, nil
}

func sourceDigest(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

type sourceModule struct {
	Path, Version, Dir, GoMod string
	Replace                   *sourceModule
}

type sourcePackageRow struct {
	ImportPath, Dir, Name, ForTest, Export                                               string
	Standard                                                                             bool
	GoFiles, CgoFiles, CompiledGoFiles                                                   []string
	TestGoFiles, XTestGoFiles                                                            []string
	CFiles, CXXFiles, MFiles, HFiles, FFiles, SFiles, SysoFiles, SwigFiles, SwigCXXFiles []string
	EmbedFiles, TestEmbedFiles, XTestEmbedFiles                                          []string
	Imports, Deps                                                                        []string
	ImportMap                                                                            map[string]string
	Module                                                                               *sourceModule
	Error                                                                                *listError
	DepsErrors                                                                           []*listError
}

// sourceGraph identifies the compile/load inputs of one package and its
// test variants, including dependency source used by the seeding walk and
// compiler exports used by type resolution. It never executes tests.
func sourceGraph(ctx context.Context, dir string, env []string, flags []string, pkg string, files *sourceFiles) (string, error) {
	args := []string{"-deps", "-test", "-compiled", "-export", "-json"}
	args = append(args, flags...)
	args = append(args, pkg)
	data, err := ownedRunner.List(ctx, dir, env, args...)
	if err != nil {
		return "", err
	}
	var rows []sourcePackageRow
	dec := json.NewDecoder(bytes.NewReader(data))
	seen := map[string]bool{}
	root := false
	inputs := map[string]string{}
	add := func(path string) error {
		if path == "" {
			return nil
		}
		digest, err := files.digest(path)
		if err != nil {
			return err
		}
		inputs[filepath.Clean(path)] = digest
		return nil
	}
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		var row sourcePackageRow
		if err := dec.Decode(&row); err == io.EOF {
			break
		} else if err != nil {
			return "", fmt.Errorf("source graph: %w", err)
		}
		if row.ImportPath == "" || seen[row.ImportPath] {
			return "", fmt.Errorf("source graph: missing or repeated package identity %q", row.ImportPath)
		}
		seen[row.ImportPath] = true
		root = root || row.ImportPath == pkg
		if row.Error != nil {
			return "", fmt.Errorf("source graph %s: %s", row.ImportPath, row.Error.Err)
		}
		if len(row.DepsErrors) > 0 {
			return "", fmt.Errorf("source graph %s: %s", row.ImportPath, row.DepsErrors[0].Err)
		}
		if err := add(row.Export); err != nil {
			return "", err
		}
		// Standard source is covered by the native selected-toolchain
		// identity. Exports still bind what the compiler supplied to types.
		if !row.Standard {
			for _, names := range [][]string{row.GoFiles, row.CgoFiles, row.CompiledGoFiles, row.TestGoFiles, row.XTestGoFiles, row.CFiles, row.CXXFiles, row.MFiles, row.HFiles, row.FFiles, row.SFiles, row.SysoFiles, row.SwigFiles, row.SwigCXXFiles, row.EmbedFiles, row.TestEmbedFiles, row.XTestEmbedFiles} {
				for _, name := range names {
					if !filepath.IsAbs(name) {
						name = filepath.Join(row.Dir, name)
					}
					if err := add(name); err != nil {
						return "", err
					}
				}
			}
		}
		for module := row.Module; module != nil; module = module.Replace {
			if err := add(module.GoMod); err != nil {
				return "", err
			}
		}
		rows = append(rows, row)
	}
	if !root {
		return "", fmt.Errorf("source graph omitted root package %q", pkg)
	}
	for _, row := range rows {
		for _, dep := range row.Deps {
			if !seen[dep] {
				return "", fmt.Errorf("source graph omitted dependency %q", dep)
			}
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ImportPath < rows[j].ImportPath })
	return sourceDigest(struct {
		Packages []sourcePackageRow
		Files    map[string]string
	}{rows, inputs})
}

// captureSourceCertificates re-derives each symbol's complete selected
// dependency set. A new capture owns new file and graph memos: closing never
// borrows the opening endpoint's reads.
func (s *Served) captureSourceCertificates(symbols []string) (map[string]resolutioncache.Certificate, error) {
	out := map[string]resolutioncache.Certificate{}
	if len(symbols) == 0 {
		return out, nil
	}
	if sourceClassifierError != nil || sourceClassifierIdentity == "" {
		return nil, fmt.Errorf("resolution classifier identity unavailable: %v", sourceClassifierError)
	}
	if !s.inventoryComplete() {
		return nil, errors.New("resolution package inventory unavailable")
	}
	current, _, err := policyBuildSelections(s.dir)
	if err != nil {
		return nil, err
	}
	if len(current) != len(s.sels) {
		return nil, errors.New("resolution policy changed during derivation")
	}
	for i := range current {
		if current[i].key() != s.sels[i].key() {
			return nil, errors.New("resolution policy changed during derivation")
		}
	}
	base, err := goworkEnv(s.dir)
	if err != nil {
		return nil, err
	}
	files := &sourceFiles{ctx: s.ctx, digests: map[string]string{}}
	bySymbol := map[string][]certificateView{}
	failed := map[string]bool{}
	var faults []error
	for _, sel := range current {
		key := sel.key()
		inventory, err := declarationPackages(s.ctx, s.dir, sel, symbols)
		if err != nil {
			return nil, err
		}
		env := selectionViewEnv(base, sel)
		reader := gotool.NewEnvReader(engineRunner, sel.directory(s.dir), env)
		snapshot, err := reader.Snapshot(s.ctx)
		if err != nil {
			return nil, err
		}
		hasher, err := closure.NewAt(s.ctx, reader, selectionViewFlags(sel)...)
		if err != nil {
			return nil, err
		}
		content, admission := hasher.ToolchainContent(), hasher.AdmissionPolicy()
		if content == "" || admission == "" {
			return nil, errors.New("resolution toolchain dependency identity unavailable")
		}
		contextFiles := map[string]string{}
		members, err := sel.members(s.dir)
		if err != nil {
			return nil, err
		}
		for _, member := range members {
			path := filepath.Join(s.dir, member, "go.mod")
			digest, err := files.digest(path)
			if err != nil {
				return nil, err
			}
			contextFiles[path] = digest
		}
		if work := snapshot.Value("GOWORK"); work != "" && work != "off" {
			digest, err := files.digest(work)
			if err != nil {
				return nil, err
			}
			contextFiles[work] = digest
		}
		graphs := map[string]string{}
		graphErrors := map[string]error{}
		for _, symbol := range symbols {
			owner, known := s.packageOf(symbol)
			if !known {
				continue
			}
			candidates := map[string]bool{}
			for _, candidate := range symbolPackageCandidates(symbol) {
				candidates[candidate] = inventory[candidate]
				if s.set != nil && inventory[candidate] != s.inventory[key][candidate] {
					return nil, errors.New("resolution package inventory changed during derivation")
				}
			}
			graph := ""
			if inventory[owner] {
				graph = graphs[owner]
				if graph == "" && graphErrors[owner] == nil {
					graph, err = sourceGraph(s.ctx, sel.directory(s.dir), env, selectionViewFlags(sel), owner, files)
					if err != nil {
						graphErrors[owner] = err
					} else {
						graphs[owner] = graph
					}
				}
				if err := graphErrors[owner]; err != nil {
					failed[symbol] = true
					faults = append(faults, fmt.Errorf("%s under selection %q: %w", symbol, key, err))
					continue
				}
			}
			bySymbol[symbol] = append(bySymbol[symbol], certificateView{key, candidates, snapshot.Identity(), content, admission, contextFiles, graph})
		}
	}
	for symbol, views := range bySymbol {
		if failed[symbol] {
			continue
		}
		inputs, err := sourceDigest(struct {
			Rule, Classifier, Symbol string
			Views                    []certificateView
		}{resolutioncache.Derivation, sourceClassifierIdentity, symbol, views})
		if err != nil {
			return nil, err
		}
		out[symbol] = resolutioncache.Certificate{Derivation: resolutioncache.Derivation, Inputs: inputs}
	}
	return out, errors.Join(faults...)
}

func sourceCertificateMoved(a, b resolutioncache.Certificate) bool {
	return a.Derivation == "" || a.Inputs == "" || a != b
}
