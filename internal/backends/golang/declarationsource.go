package golang

import (
	"fmt"
	"go/ast"
	"go/types"

	"github.com/greatliontech/go-x-tools/go/packages"
)

type declarationSource struct {
	name string
	file *ast.File
}

type sourceLoadKey struct {
	config *packages.Config
	path   string
}

type sourceLoad struct {
	pkg *packages.Package
	err error
}

// declarationSource recovers physical source under the resolving load's
// selection. Export data preserves diagnostic positions, not the original
// filename behind a //line directive, so a promoted declaration outside the
// parsed roots is resolved again in its own package before reading its file.
func (b *Backend) declarationSource(resolving *packages.Package, obj types.Object) (declarationSource, error) {
	if fn, ok := obj.(*types.Func); ok {
		obj = fn.Origin()
	}
	if v, ok := obj.(*types.Var); ok && v.IsField() {
		obj = v.Origin()
	}
	if !obj.Pos().IsValid() || obj.Pkg() == nil {
		return declarationSource{}, nil
	}
	for _, pkg := range b.pkgs {
		if b.load[pkg] == b.load[resolving] && pkg.PkgPath == obj.Pkg().Path() {
			if source, ok := physicalDeclaration(pkg, obj); ok {
				return source, nil
			}
		}
	}
	var cfg *packages.Config
	for member := range memberPatterns(b.selectionModules(b.selectionOf(resolving)), []string{variantBase(resolving)}) {
		cfg = b.lazyCfg[member][b.selectionOf(resolving)]
	}
	if cfg == nil {
		return declarationSource{}, fmt.Errorf("declaring source of %s: no configuration for its resolving view", obj.Name())
	}
	pkg, err := b.sourcePackage(cfg, obj.Pkg().Path())
	if err != nil {
		return declarationSource{}, fmt.Errorf("declaring source of %s: %w", obj.Name(), err)
	}
	parts := []string{obj.Name()}
	if fn, ok := obj.(*types.Func); ok {
		sig := fn.Type().(*types.Signature)
		if recv := sig.Recv(); recv != nil {
			typ := types.Unalias(recv.Type())
			if ptr, ok := typ.(*types.Pointer); ok {
				typ = types.Unalias(ptr.Elem())
			}
			if named, ok := typ.(*types.Named); ok {
				parts = []string{named.Obj().Name(), obj.Name()}
			}
		}
		if len(parts) == 1 && obj.Pkg().Scope().Lookup(obj.Name()) != obj {
			// An alias of an anonymous interface has no named receiver.
			// Find a package type that exposes this exact method object,
			// not a same-shaped method of a different interface.
			for _, name := range obj.Pkg().Scope().Names() {
				tn, ok := obj.Pkg().Scope().Lookup(name).(*types.TypeName)
				if !ok {
					continue
				}
				ms := types.NewMethodSet(tn.Type())
				for i := 0; i < ms.Len(); i++ {
					if ms.At(i).Obj().(*types.Func).Origin() == fn {
						parts = []string{name, obj.Name()}
						break
					}
				}
				if len(parts) == 2 {
					break
				}
			}
		}
	} else if v, ok := obj.(*types.Var); ok && v.IsField() {
		for _, name := range obj.Pkg().Scope().Names() {
			tn, ok := obj.Pkg().Scope().Lookup(name).(*types.TypeName)
			if !ok {
				continue
			}
			typ := tn.Type()
			target := name
			if _, ok := typ.Underlying().(*types.Struct); !ok {
				ptr, ok := typ.Underlying().(*types.Pointer)
				if !ok {
					continue
				}
				named, ok := types.Unalias(ptr.Elem()).(*types.Named)
				if !ok || named.Obj().Pkg() != obj.Pkg() {
					continue
				}
				if _, ok := named.Underlying().(*types.Struct); !ok {
					continue
				}
				typ = named
				target = named.Obj().Name()
			}
			if got, _, _ := types.LookupFieldOrMethod(typ, true, obj.Pkg(), obj.Name()); got != nil {
				if gotVar, ok := got.(*types.Var); ok && gotVar.IsField() && gotVar.Origin() == v {
					parts = []string{target, obj.Name()}
					break
				}
			}
		}
	}
	declared := lookup(pkg.Types, parts)
	if declared != nil && declared.Pkg() != nil && declared.Pkg().Path() == obj.Pkg().Path() && shapeHash(declared) == shapeHash(obj) {
		if source, ok := physicalDeclaration(pkg, declared); ok {
			return source, nil
		}
	}
	return declarationSource{}, fmt.Errorf("declaring source of %s in %s no longer agrees with its loaded declaration", obj.Name(), obj.Pkg().Path())
}

func physicalDeclaration(pkg *packages.Package, obj types.Object) (declarationSource, bool) {
	pos := obj.Pos()
	for _, file := range pkg.Syntax {
		if file.FileStart <= pos && pos < file.FileEnd {
			return declarationSource{pkg.Fset.PositionFor(pos, false).Filename, file}, true
		}
	}
	return declarationSource{}, false
}

// sourcePackage loads a declaring package once per configuration. It never
// extends the seeding walk's declaration index: reading a dependency's marker
// is not permission to walk its bodies for witness classification.
func (b *Backend) sourcePackage(cfg *packages.Config, path string) (*packages.Package, error) {
	b.sourceMu.Lock()
	defer b.sourceMu.Unlock()
	if err := cfg.Context.Err(); err != nil {
		return nil, err
	}
	key := sourceLoadKey{cfg, path}
	if load, ok := b.sourceLoads[key]; ok {
		return load.pkg, load.err
	}
	loaded, failures, err := loadTypedPackages(cfg, path)
	var found *packages.Package
	if err == nil {
		for _, pkg := range loaded {
			if pkg.PkgPath != path {
				continue
			}
			if len(pkg.Errors) > 0 {
				if failure := failures[pkg]; failure != nil {
					err = fmt.Errorf("package %s has load errors: %w", path, failure)
				} else {
					err = fmt.Errorf("package %s: %v", path, pkg.Errors[0])
				}
			} else {
				found = pkg
			}
			break
		}
		if found == nil && err == nil {
			err = fmt.Errorf("package %s has no loaded declaring source", path)
		}
	}
	if cancelled := cfg.Context.Err(); cancelled != nil {
		return nil, cancelled
	}
	if b.sourceLoads == nil {
		b.sourceLoads = map[sourceLoadKey]sourceLoad{}
	}
	b.sourceLoads[key] = sourceLoad{found, err}
	return found, err
}
