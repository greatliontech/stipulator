package golang

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/greatliontech/gofresh/gotool"
	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/backends/golang/internal/test2json"
)

type retainedRuntimeOptions struct {
	Package string
	// Environment is the same prospective environment used for warm checking.
	// Runtime preparation stages metadata and arguments without replacing it.
	Environment *retainedEnvironment
	// GOCACHE is the compilation's resolved Go environment snapshot value.
	GOCACHE   string
	Selection []string
	LogPath   string
	Coverage  *retainedCoverageProfile
	// With -coverpkg, every selected package must finish compiling before
	// execution. This is the complete artifact set, including build-only
	// packages: Go puts their static zero-count metadata in each binary's
	// profile. Keep every artifact alive until all attempts have finished.
	CoverageArtifacts []*retainedArtifact
}

// retainedRuntime is the complete description the caller binds BEFORE native
// registration. Each attempt owns a fresh runtime and coverage directory even
// when the artifact is reused. No native support is required to execute it.
type retainedRuntime struct {
	*retainedEnvironment
	Program          string
	Args             []string
	Package          string
	CoverageFragment string
	profile          *retainedCoverageProfile
	artifact         *retainedArtifact
}

func (r *retainedRuntime) Close() error {
	if r == nil {
		return nil
	}
	return r.retainedEnvironment.Close()
}

func prepareRetainedRuntime(ctx context.Context, n *NormalizedInvocation, a *retainedArtifact, o retainedRuntimeOptions, hooks retainedProcessHooks) (*retainedRuntime, error) {
	if a == nil || a.Path == "" {
		return nil, errors.New("package has no retained executable")
	}
	if o.Environment == nil {
		return nil, errors.New("runtime requires its prepared prospective environment")
	}
	r := &retainedRuntime{retainedEnvironment: o.Environment, Program: a.Path, Package: o.Package, profile: o.Coverage, artifact: a}
	r.Args = r.flags.runtimeArgs(n, o.Selection, o.LogPath)
	var extra []string
	if r.flags.values["fuzz"] != "" {
		if !filepath.IsAbs(o.GOCACHE) {
			return nil, errors.New("fuzz execution needs the resolved absolute GOCACHE")
		}
		extra = append(extra, "-test.fuzzcachedir="+filepath.Join(o.GOCACHE, "fuzz", o.Package))
	}
	if r.flags.cover {
		extra = append(extra, "-test.gocoverdir="+r.CoverageDir)
		if r.flags.coverPkg != "" {
			if len(o.CoverageArtifacts) == 0 {
				return nil, errors.New("coverpkg runtime needs the complete selected package artifact set")
			}
			if err := retainedCoverageMetadata(ctx, n, o.CoverageArtifacts, r.CoverageDir, hooks); err != nil {
				return nil, err
			}
		}
	}
	// cmd/go prepends fuzz and coverage infrastructure before testFlags' output.
	r.Args = append(append(append([]string{}, r.Args[:1]...), extra...), r.Args[1:]...)
	if r.flags.values["coverprofile"] != "" {
		if o.Coverage == nil {
			return nil, errors.New("GOFLAGS coverprofile requires an invocation-owned profile merger")
		}
		if r.root != "" {
			r.CoverageFragment = filepath.Join(r.root, "cover.out")
		} else {
			// A later -cover=false can leave a requested profile on an
			// uninstrumented binary. Preserve its ordinary testing error,
			// without injecting coverage into the already-admitted environment.
			f, err := os.CreateTemp(a.root, "cover-*.out")
			if err != nil {
				return nil, err
			}
			r.CoverageFragment = f.Name()
			if err := f.Close(); err != nil {
				return nil, err
			}
		}
		// cmd/go rewrites even raw single-token coverprofile overrides, but
		// leaves two-token raw arguments and positional parsing to the binary.
		for i, arg := range r.Args {
			if strings.HasPrefix(arg, "-test.coverprofile=") {
				r.Args[i] = "-test.coverprofile=" + r.CoverageFragment
			}
		}
	}
	return r, nil
}

// retainedCoverageMetadata supplies unexecuted packages' static coverage
// fragments in Go's own metafiles encoding. Linked packages are ignored by the
// testing runtime when already accounted for in its live metadata. The -work
// compile retains these files even when the compiler reused its build cache.
func retainedCoverageMetadata(ctx context.Context, n *NormalizedInvocation, artifacts []*retainedArtifact, dir string, hooks retainedProcessHooks) error {
	var collection struct{ ImportPaths, MetaFileFragments []string }
	seen := map[string]bool{}
	selected := map[string]bool{}
	for _, a := range artifacts {
		if a != nil {
			for _, pkg := range a.coveragePackages {
				selected[pkg] = true
			}
		}
	}
	seenWork := map[string]bool{}
	for _, a := range artifacts {
		if a == nil || a.work == "" {
			return errors.New("coverage compilation did not retain its WORK metadata directory")
		}
		if seenWork[a.work] {
			continue
		}
		seenWork[a.work] = true
		err := filepath.WalkDir(a.work, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasPrefix(d.Name(), "covmeta.") {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			if info.Size() == 0 {
				return nil
			}
			out, err := retainedTool(ctx, n, hooks, "tool", "covdata", "pkglist", "-i="+filepath.Dir(path))
			if err != nil {
				return err
			}
			for pkg := range strings.Lines(string(out)) {
				pkg = strings.TrimSpace(pkg)
				if selected[pkg] && !seen[pkg] {
					seen[pkg] = true
					collection.ImportPaths = append(collection.ImportPaths, pkg)
					collection.MetaFileFragments = append(collection.MetaFileFragments, path)
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	if len(collection.ImportPaths) == 0 {
		return nil
	}
	data, err := json.Marshal(collection)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "metafiles.txt"), data, 0600)
}

// mergeRetainedBuildCoverage handles a covered package with no test files. It
// invokes only Go's coverage utility, never a fabricated test process.
func mergeRetainedBuildCoverage(ctx context.Context, n *NormalizedInvocation, a *retainedArtifact, profile *retainedCoverageProfile, hooks retainedProcessHooks) error {
	if a == nil || !a.flags.cover || profile == nil {
		return nil
	}
	if a.work == "" {
		return errors.New("build-only coverage has no retained metadata")
	}
	var dirs []string
	err := filepath.WalkDir(a.work, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasPrefix(d.Name(), "covmeta.") {
			info, err := d.Info()
			if err != nil {
				return err
			}
			if info.Size() != 0 {
				dirs = append(dirs, filepath.Dir(path))
			}
		}
		return nil
	})
	if err != nil || len(dirs) == 0 {
		return err
	}
	fragment := filepath.Join(a.root, "build-cover.out")
	if _, err = retainedTool(ctx, n, hooks, "tool", "covdata", "textfmt", "-i="+strings.Join(dirs, ","), "-pkg="+a.Package, "-o="+fragment); err != nil {
		return err
	}
	return profile.Merge(fragment)
}

type retainedRunResult struct {
	Producer *stipulatorv1.ProducerIdentity
	Stream   *streamState
	// HarnessTerminal is the ORIGINAL converter terminal, before health is
	// normalized to the binary exit. Silent TestMain has no harness terminal.
	HarnessTerminal                      string
	BinaryState                          *os.ProcessState
	BinaryErr, ConversionErr, ProfileErr error
	// ProfileErr includes final profile merging and the requested profiling
	// companion (or explicit -o) installation; neither rewrites binary facts.
	OwnershipErr error
	Interrupted  bool
}

// HarnessComplete is only an execution fact, never native observation proof.
// In particular a synthesized health event, signal, panic after FAIL, unfinished
// test, failed capture flush or poisoned conversion cannot establish completion.
func (r retainedRunResult) HarnessComplete() bool {
	if r.Interrupted || r.BinaryState == nil || !r.BinaryState.Exited() || r.OwnershipErr != nil || r.ConversionErr != nil || r.Stream == nil || r.Stream.malformed != "" || r.Stream.postTerminal || r.Stream.sawAbort || len(r.Stream.started) != 0 {
		return false
	}
	return r.HarnessTerminal == "pass" && r.BinaryState.ExitCode() == 0 && r.BinaryErr == nil || r.HarnessTerminal == "fail" && r.BinaryState.ExitCode() == 1
}

// Classify projects package health from the binary's actual terminal state.
// Original harness completion remains separate and unmodified on the result.
func (r retainedRunResult) Classify(n *NormalizedInvocation, pkg string) packageRun {
	if r.Stream == nil {
		if r.Interrupted {
			return packageRun{pkg: pkg}
		}
		return degradedRun(n.Name, pkg, fmt.Sprintf("starting retained execution: %v", errors.Join(r.BinaryErr, r.ConversionErr)), false)
	}
	if r.Interrupted {
		return packageRun{pkg: pkg, producer: r.Producer, aborted: startedTests(r.Stream), residue: runResidue(r.Stream, &boundedBuffer{}, r.BinaryErr, ""), peakBytes: processPeakBytes(r.BinaryState)}
	}
	st := *r.Stream
	if err := errors.Join(r.OwnershipErr, r.ConversionErr, r.ProfileErr); err != nil {
		st.malformed = "retained output processing: " + err.Error() + "; " + st.malformed
	}
	if r.BinaryState == nil {
		st.malformed = fmt.Sprintf("test binary did not start: %v", r.BinaryErr)
	} else if r.BinaryState.Success() {
		st.terminal = "pass"
	} else {
		st.terminal = "fail"
	}
	run := classifyRun(n.Name, pkg, &st, r.BinaryErr, &boundedBuffer{}, declaredBinaryBound(n))
	run.producer, run.aborted, run.peakBytes = r.Producer, startedTests(r.Stream), processPeakBytes(r.BinaryState)
	return run
}

// retainedOutputForTest injects reader/writer failures at the owned conversion
// boundary without manufacturing a converter process or process outcome.
var retainedOutputForTest func(io.Reader, io.Writer) (io.Reader, io.Writer)

func convertRetainedOutput(dst io.Writer, src io.Reader, pkg string) error {
	c := test2json.NewConverter(dst, pkg, test2json.Timestamp)
	if _, err := c.Write(nil); err != nil {
		return err
	}
	_, err := io.Copy(c, src)
	return errors.Join(err, c.Close())
}

// runRetained starts no compiler and grants no native proof. The caller has
// registered its settled runtime before entering this function. caller is the
// original operation context, distinct from the possibly expired envelope ctx.
// Binary stdout and stderr share one OS pipe. Forwarding preserves EOF's
// diagnostic-only fragment while flushing already-parsed test reports.
// Binary termination and in-process conversion are independent facts. Only the
// actual binary process is registered to the package's execution slot.
func runRetained(caller, ctx context.Context, n *NormalizedInvocation, r *retainedRuntime, producer *stipulatorv1.ProducerIdentity, hooks retainedProcessHooks) (result retainedRunResult) {
	defer func() { result.Interrupted = ctx.Err() != nil || caller.Err() != nil }()
	if err := ctx.Err(); err != nil {
		result.BinaryErr = err
		return
	}
	if err := ensureTelemetryOwned(n.Env, n.TelemetrySource); err != nil {
		result.BinaryErr = err
		return
	}
	binCtx, cancelBinary := context.WithCancelCause(context.WithoutCancel(ctx))
	defer cancelBinary(context.Canceled)
	// The driver owns the quit/grace schedule, so a later caller cancellation
	// can escalate an expired envelope immediately. The runner still owns
	// each process group and every actual context-cancellation sweep.
	binaryRunner := gotool.Runner{Containment: &gotool.Containment{}, Prepare: observeChild}
	binary, err := binaryRunner.Program(binCtx, r.Dir, r.Env, r.Program, r.Args...)
	if err != nil {
		result.BinaryErr = err
		return
	}
	binaryOutput, output, err := os.Pipe()
	if err != nil {
		result.BinaryErr = err
		return
	}
	defer binaryOutput.Close()
	defer output.Close()
	// Caller cancellation closes the local read as well as killing the owned
	// tree. An escaped pipe holder cannot keep the operation blocked in Read.
	stopCaller := context.AfterFunc(caller, func() {
		cancelBinary(context.Cause(caller))
		_ = binaryOutput.Close()
	})
	defer stopCaller()
	jsonRead, jsonWrite := io.Pipe()
	defer jsonRead.Close()
	defer jsonWrite.Close()
	binary.Stdout, binary.Stderr = output, output
	finishBinary, startErr := startRetainedCommand(binCtx, binary, "binary", hooks)
	if startErr == nil {
		defer finishBinary.Close()
		result.Producer = producer
		if producer != nil {
			producer.SetProcessId(int64(binary.Process.Pid))
		}
	}
	finished := make(chan struct{})
	defer close(finished)
	stopEnvelope := context.AfterFunc(ctx, func() {
		cause := context.Cause(ctx)
		if !errors.Is(cause, errEnvelopeExpired) || finishBinary == nil {
			cancelBinary(cause)
			_ = binaryOutput.Close()
			return
		}
		if !finishBinary.Quit() {
			cancelBinary(cause)
		}
		timer := time.NewTimer(quitGrace)
		defer timer.Stop()
		select {
		case <-timer.C:
			cancelBinary(cause)
		case <-finished:
			return
		}
		timer.Reset(gotool.DefaultWaitDelay)
		select {
		case <-timer.C:
			_ = binaryOutput.Close()
		case <-finished:
		}
	})
	defer stopEnvelope()
	parsed := make(chan *streamState, 1)
	go func() { parsed <- parseTestStream(n.Name, r.Package, jsonRead, producer) }()
	converted := make(chan error, 1)
	go func() {
		var src io.Reader = binaryOutput
		var dst io.Writer = jsonWrite
		if retainedOutputForTest != nil {
			src, dst = retainedOutputForTest(src, dst)
		}
		err := convertRetainedOutput(dst, src, r.Package)
		if err != nil {
			cancelBinary(err)
		}
		_ = jsonWrite.Close()
		converted <- err
	}()
	binaryExited := make(chan error, 1)
	if startErr == nil && retainedCanObserveExit {
		go func() {
			err := finishBinary.Exited()
			_ = output.Close()
			binaryExited <- err
		}()
	} else {
		_ = output.Close()
		binaryExited <- startErr
	}
	result.Stream = <-parsed
	result.ConversionErr = <-converted
	result.BinaryErr = <-binaryExited
	if startErr == nil {
		_ = finishBinary.Wait()
		result.OwnershipErr = finishBinary.ownershipErr
		result.BinaryErr = finishBinary.reapErr
		result.BinaryState = binary.ProcessState
	}
	result.HarnessTerminal = result.Stream.terminal
	if result.Stream.malformed != "" || result.Stream.postTerminal || result.Stream.events == 0 {
		result.ConversionErr = errors.Join(result.ConversionErr, errors.New("output conversion did not produce a complete valid event stream"))
	}
	result.Interrupted = ctx.Err() != nil
	if r.CoverageFragment != "" {
		result.ProfileErr = r.profile.Merge(r.CoverageFragment)
	}
	if result.BinaryState != nil && !result.Interrupted && caller.Err() == nil {
		result.ProfileErr = errors.Join(result.ProfileErr, r.artifact.publishCompanion())
	}
	return
}
