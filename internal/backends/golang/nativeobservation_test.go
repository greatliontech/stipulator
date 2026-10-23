package golang

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/runtimeinput"
	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
)

// observationFrame is an identity-only native transaction for guard fixtures.
// These fixtures supply operation logs deliberately; actual harness completion
// and supported-package claims are exercised by retained/native integration tests.
type observationFrame struct {
	unit         *gofresh.Unit
	contribution gofresh.Contribution
	capture      string
	err          error
}

func captureObservationFrame(ctx context.Context, n *NormalizedInvocation, pkg string) (frame observationFrame) {
	dir := n.PkgDirs[pkg]
	if dir == "" {
		frame.err = fmt.Errorf("package directory unknown at spawn")
		return
	}
	path := filepath.Join(dir, "native_observation_probe.go")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := os.WriteFile(path, []byte("package pkg\nfunc NativeObservationProbe() {}\n"), 0600); err != nil {
			frame.err = err
			return
		}
	}
	subject := gofresh.Subject{Package: pkg, Symbol: "NativeObservationProbe"}
	session, err := gofresh.NewSession(ctx, gofresh.BuildDescription{Directory: n.Dir, Environment: append([]string{}, n.Env...), Inputs: []string{}}, gofresh.SessionPolicy{EvidenceRoot: treeRoot(n), Runner: engineRunner})
	if err != nil {
		frame.err = err
		return
	}
	snapshot, err := session.Snapshot(ctx, []gofresh.Subject{subject}, gofresh.CodeResult)
	if err != nil {
		frame.err = err
		return
	}
	binding, err := snapshot.BindRuntime(ctx, gofresh.RuntimeDescription{Directory: dir, Environment: witnessEnvOf(n)})
	if err != nil {
		frame.err = err
		return
	}
	frame.unit, err = binding.BeginIdentity(subject)
	if err != nil {
		frame.err = err
		return
	}
	built, err := snapshot.BindBuilt(pkg, "guard-fixture")
	if err != nil {
		frame.err = err
		return
	}
	f, err := os.CreateTemp("", "stipulator-native-guard-*")
	if err != nil {
		frame.err = err
		return
	}
	frame.capture = f.Name()
	_ = f.Close()
	brackets, refusal := bracketRootsFor(n, pkg)
	if refusal != "" {
		frame.err = fmt.Errorf("%s", refusal)
		return
	}
	frame.contribution, frame.err = frame.unit.Register(ctx, built, gofresh.ProcessDescription{Identity: "guard-fixture", Capture: frame.capture, BracketPaths: brackets, ExcludedPaths: n.ExcludedPaths, ScratchNamespaces: n.ScratchNamespaces})
	return
}

func observeProcess(ctx context.Context, n *NormalizedInvocation, pkg string, producer *stipulatorv1.ProducerIdentity, st *streamState, waitErr error, disposition stipulatorv1.HealthDisposition, logPath string, frame observationFrame) *ProcessObservation {
	if frame.unit != nil {
		defer frame.unit.Abort()
	}
	if frame.capture != "" {
		defer os.Remove(frame.capture)
	}
	if frame.err != nil {
		return incompleteObservation(pkg, producer, "observation construction failed: "+frame.err.Error())
	}
	data, err := os.ReadFile(logPath)
	if err == nil {
		err = os.WriteFile(frame.capture, data, 0600)
	}
	if err != nil {
		return incompleteObservation(pkg, producer, "observation construction failed: "+err.Error())
	}
	terminal := gofresh.Terminal{Kind: gofresh.NormalCompletion}
	if reason := incompleteObservationReason(st, waitErr, disposition); reason != "" {
		terminal = gofresh.Terminal{Kind: gofresh.AbnormalCompletion, Reason: reason}
	}
	if err := frame.unit.Submit(frame.contribution, terminal); err != nil {
		return incompleteObservation(pkg, producer, err.Error())
	}
	_, err = frame.unit.Close(ctx)
	account, _ := frame.unit.Account()
	if err != nil {
		err = fmt.Errorf("observation construction failed: %w", err)
	}
	return nativeObservation(pkg, producer, gofresh.FinalizedProcess{}, account, err)
}

func processObservationState(o *ProcessObservation) (runtimeinput.State, error) {
	if o.account.Inputs.Manifest == "" {
		return runtimeinput.State{}, fmt.Errorf("observation has no finalized input account: %s", o.Wire.GetIncompleteReason())
	}
	return o.account.Inputs, nil
}
