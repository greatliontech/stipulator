package golang

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
)

// retainedCompanionPath is cmd/go's user-visible test-binary destination. Raw
// binary profile flags do not request a companion; cmd/go's profiling flags
// (excluding trace), or its explicit -o, do. Profiles' outputdir does not move
// this binary: the default is the invocation directory.
func retainedCompanionPath(n *NormalizedInvocation, pkg string, f retainedFlags) string {
	needed := f.output != ""
	for _, flag := range []string{"blockprofile", "cpuprofile", "memprofile", "mutexprofile"} {
		needed = needed || f.values[flag] != ""
	}
	if !needed || retainedNullOutput(f.output) {
		return ""
	}
	name := path.Base(pkg)
	if pkg == "command-line-arguments" {
		for _, p := range n.selectedPackages {
			if p.ImportPath == pkg {
				name = p.Name
				break
			}
		}
	} else {
		moduleMode, _ := lookupEnv(witnessEnvOf(n), "GO111MODULE")
		if moduleMode != "off" && name != pkg && len(name) > 1 && name[0] == 'v' && name[1] != '0' && name != "v1" && strings.Trim(name[1:], "0123456789") == "" {
			name = path.Base(path.Dir(pkg))
		}
	}
	name += ".test"
	if n.GOOS == "windows" {
		name += ".exe"
	}
	if f.output == "" {
		return filepath.Join(n.Dir, name)
	}
	destination := f.output
	if !filepath.IsAbs(destination) {
		destination = filepath.Join(n.Dir, destination)
	}
	if retainedOutputDirectory(n, f.output) {
		destination = filepath.Join(destination, name)
	}
	return destination
}

func retainedOutputDirectory(n *NormalizedInvocation, output string) bool {
	if strings.HasSuffix(output, "/") || strings.HasSuffix(output, string(os.PathSeparator)) {
		return true
	}
	if !filepath.IsAbs(output) {
		output = filepath.Join(n.Dir, output)
	}
	info, err := os.Stat(output)
	return err == nil && info.IsDir()
}

func retainedNullOutput(output string) bool {
	return output == os.DevNull || runtime.GOOS == "windows" && strings.EqualFold(output, "NUL")
}

func retainedScopeCompanions(n *NormalizedInvocation, packages []string, f retainedFlags) (map[string]string, error) {
	if len(packages) > 1 && f.output != "" && !retainedNullOutput(f.output) && !retainedOutputDirectory(n, f.output) {
		return nil, fmt.Errorf("with multiple packages, -o must refer to a directory or %s", os.DevNull)
	}
	paths := map[string]string{}
	owner := map[string]string{}
	for _, pkg := range packages {
		p := retainedCompanionPath(n, pkg, f)
		if p != "" {
			if previous := owner[p]; previous != "" {
				return nil, fmt.Errorf("cannot write test binary %s for multiple packages: %s, %s", p, previous, pkg)
			}
			owner[p] = pkg
		}
		paths[pkg] = p
	}
	return paths, nil
}

// publishCompanion copies the executed private artifact after the attempt, as
// Go's install action follows its test action. The public copy is not owned by
// artifact cleanup. Output errors affect package health, not the binary status.
func (a *retainedArtifact) publishCompanion() error {
	if a == nil || a.publicPath == "" || a.Path == "" {
		return nil
	}
	dst := a.publicPath
	if info, err := os.Stat(dst); err == nil {
		if info.IsDir() {
			return fmt.Errorf("build output %q already exists and is a directory", dst)
		}
		if info.Mode().IsRegular() && info.Size() != 0 && !retainedObjectFile(dst) {
			return fmt.Errorf("build output %q already exists and is not an object file", dst)
		}
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0777); err != nil {
		return err
	}
	in, err := os.Open(a.Path)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.CreateTemp(filepath.Dir(dst), ".stipulator-test-binary-*")
	if err != nil {
		return err
	}
	tmp := out.Name()
	defer os.Remove(tmp)
	if _, err = io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err = out.Chmod(info.Mode().Perm()); err != nil {
		out.Close()
		return err
	}
	if err = out.Close(); err != nil {
		return err
	}
	if err = os.Rename(tmp, dst); err != nil && runtime.GOOS == "windows" {
		// Match Go's replacement of an existing executable on Windows.
		if renameErr := os.Rename(dst, dst+"~"); renameErr == nil {
			_ = os.Remove(dst + "~")
		}
		err = os.Rename(tmp, dst)
	}
	return err
}

// cmd/go's overwrite check accepts object-format prefixes, not arbitrary
// nonempty files. Keep this guard when exporting a profiling companion.
func retainedObjectFile(name string) bool {
	f, err := os.Open(name)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 64)
	_, _ = io.ReadFull(f, buf)
	for _, magic := range []string{"!<arch>\n", "<bigaf>\n", "\x7fELF", "\xfe\xed\xfa\xce", "\xfe\xed\xfa\xcf", "\xce\xfa\xed\xfe", "\xcf\xfa\xed\xfe", "MZ\x90\x00\x03\x00", "MZ\x78\x00\x01\x00", "\x00\x00\x01\xeb", "\x00\x00\x8a\x97", "\x00\x00\x06\x47", "\x00asm", "\x01\xdf", "\x01\xf7"} {
		if bytes.HasPrefix(buf, []byte(magic)) {
			return true
		}
	}
	return false
}
