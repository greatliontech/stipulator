package golang

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"

	"github.com/greatliontech/gofresh/gotool"
)

// retainedEnvironment owns one prospective attempt's complete runtime
// environment and output storage. Prepare it before warm native checking; if
// execution is needed, pass this same object to prepareRetainedRuntime. Each
// isolation attempt prepares its own object. Fields are immutable after return.
type retainedEnvironment struct {
	Dir         string
	Env         []string
	CoverageDir string
	root        string
	flags       retainedFlags
}

// prepareRetainedEnvironment needs neither an artifact nor a Go subprocess.
// goroot is the compilation description's resolved GOROOT. Coverage gets its
// real prospective private directory now, so checking cannot accidentally use
// ambient GOCOVERDIR while execution later substitutes another value.
func prepareRetainedEnvironment(n *NormalizedInvocation, dir, goroot string) (*retainedEnvironment, error) {
	if !filepath.IsAbs(dir) || !filepath.IsAbs(goroot) {
		return nil, errors.New("runtime package directory and resolved GOROOT must be absolute")
	}
	flags, err := retainedGOFLAGS(n)
	if err != nil {
		return nil, err
	}
	env, err := gotool.EnvForCommand(witnessEnvOf(n), dir)
	if err != nil {
		return nil, err
	}
	e := &retainedEnvironment{Dir: dir, Env: env, flags: flags}
	pathKey := "PATH"
	if runtime.GOOS == "plan9" {
		pathKey = "path"
	}
	path, _ := lookupEnv(e.Env, pathKey)
	if path != "" {
		path = string(os.PathListSeparator) + path
	}
	e.Env = gotool.SetEnv(e.Env, pathKey, filepath.Join(goroot, "bin")+path)
	// Without coverage there is no output storage to allocate: ordinary
	// execution still works when the caller could not create a capture log.
	if flags.cover {
		e.root, err = os.MkdirTemp("", "stipulator-runtime-*")
		if err != nil {
			return nil, err
		}
		e.CoverageDir = filepath.Join(e.root, "gocoverdir")
		if err = os.Mkdir(e.CoverageDir, 0700); err != nil {
			_ = e.Close()
			return nil, err
		}
		e.Env = gotool.SetEnv(e.Env, "GOCOVERDIR", e.CoverageDir)
	}
	return e, nil
}

// Close releases the attempt's storage after warm serving or after execution
// and native finalization. A runtime built from e shares this same ownership.
func (e *retainedEnvironment) Close() error {
	if e == nil || e.root == "" {
		return nil
	}
	return os.RemoveAll(e.root)
}
