package golang

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"

	gofresh "github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/gotool"
	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
)

// ProcessObservation pairs the public, non-authorizing process account with the
// native result of this exact attempt. A failed close retains observed facts but
// exposes no subject evidence. No sealed observation capability crosses this
// consumer boundary.
type ProcessObservation struct {
	Wire     *stipulatorv1.Observation
	final    gofresh.FinalizedProcess
	account  gofresh.ProcessAccount
	closeErr error
	runtime  gofresh.RuntimeDescription
}

// nativeCloseCauses retains both demonstrated runtime movement and other faults.
// Ordinary publication can localize only an exclusively runtime-movement error;
// retry validation must still notice established movement beside another fault.
// Wrapping preserves the cause, and every branch of a join is inspected, never
// just the first match errors.As could find. Diagnostics grant no classification.
func nativeCloseCauses(err error) (movement, fault bool) {
	if err == nil {
		return false, false
	}
	fault = errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
	causal, hasCause := err.(interface{ Cause() gofresh.Cause })
	if hasCause {
		movement = causal.Cause() == (gofresh.Cause{Kind: gofresh.RuntimeInputsCause, Condition: gofresh.Changed})
		fault = fault || !movement
	}
	var children []error
	switch wrapped := err.(type) {
	case interface{ Unwrap() []error }:
		children = wrapped.Unwrap()
	case interface{ Unwrap() error }:
		children = []error{wrapped.Unwrap()}
	}
	found := false
	for _, child := range children {
		if child == nil {
			continue
		}
		found = true
		childMovement, childFault := nativeCloseCauses(child)
		movement = movement || childMovement
		fault = fault || childFault
	}
	return movement, fault || !hasCause && !found
}

func runtimeInputMovement(err error) bool {
	movement, fault := nativeCloseCauses(err)
	return movement && !fault
}

func (o *ProcessObservation) subject(s gofresh.Subject) (gofresh.FinalizedSubject, bool) {
	if o != nil && o.closeErr == nil {
		for _, result := range o.final.Subjects {
			if result.Subject == s {
				return result, true
			}
		}
	}
	return gofresh.FinalizedSubject{}, false
}

func nativeObservation(pkg string, producer *stipulatorv1.ProducerIdentity, final gofresh.FinalizedProcess, account gofresh.ProcessAccount, closeErr error) *ProcessObservation {
	o := &ProcessObservation{final: final, account: account, closeErr: closeErr, Wire: &stipulatorv1.Observation{}}
	o.Wire.SetProducer(producer)
	o.Wire.SetPackage(pkg)
	if account.Observation == gofresh.SupportedObservation || account.Observation == gofresh.IdentityObservation {
		completed := &stipulatorv1.CompletedObservation{}
		completed.SetManifest(account.Inputs.Manifest)
		completed.SetOutcomeReason(account.OutcomeReason)
		if !account.Inputs.Unverifiable {
			completed.SetDigest(account.Inputs.Digest)
		}
		o.Wire.SetCompleted(completed)
	} else {
		reason := account.ObservationReason
		if reason == "" && closeErr != nil {
			reason = closeErr.Error()
		}
		if reason == "" {
			reason = "native observation not finalized"
		}
		o.Wire.SetIncompleteReason(reason)
	}
	return o
}

// bracketRootsFor preserves the discovery-owned compilation closure beside the
// reviewed roots. An unavailable closure cannot silently narrow the bracket.
func bracketRootsFor(n *NormalizedInvocation, pkg string) ([]string, string) {
	if n.ClosureDirsErr != "" {
		return nil, "package import closure unresolved at spawn (" + n.ClosureDirsErr + "); no observation bracket was captured"
	}
	closure, ok := n.PkgClosureDirs[pkg]
	if !ok {
		return nil, "package import closure unknown at spawn; no observation bracket was captured"
	}
	roots := append(append([]string(nil), closure...), n.BracketPaths...)
	return append(roots, inputFilePaths(n.InputFiles)...), ""
}

// incompleteObservationReason explains the stream/exit portion of an incomplete
// harness. The retained driver separately checks the original terminal event,
// conversion, ownership and actual binary status before asserting completion.
func incompleteObservationReason(st *streamState, waitErr error, disposition stipulatorv1.HealthDisposition) string {
	failed := disposition == stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TEST_FAILED && st.terminal == "fail"
	if disposition != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_HEALTHY && !failed {
		return fmt.Sprintf("package disposed %s, not HEALTHY; the testlog flush is unproven", disposition)
	}
	if st.terminal != "pass" && !failed {
		return "no test process ran (terminal " + st.terminal + ")"
	}
	if st.sawAbort {
		return "abort output observed in the stream; the testlog flush cannot be trusted"
	}
	if len(startedTests(st)) > 0 {
		return "tests started but unfinished; the process died before its testlog flushed"
	}
	if failed {
		var exit *exec.ExitError
		if !errors.As(waitErr, &exit) || exit.ExitCode() != 1 {
			return "failing test process has no ordinary exit status 1"
		}
		for _, test := range st.tests {
			if test.GetOutcome() == stipulatorv1.TestOutcome_TEST_OUTCOME_FAILED {
				return ""
			}
		}
		return "failing process has no completed failing test; the testlog flush is unproven"
	}
	if waitErr != nil {
		return fmt.Sprintf("process exited with failure: %v", waitErr)
	}
	return ""
}

// witnessEnvOf reads the once-derived compilation environment. The fallback is
// for hand-built test invocations; production execution finalizes allocation
// before capture or spawn.
func witnessEnvOf(n *NormalizedInvocation) []string {
	if n.WitnessEnv != nil {
		return n.WitnessEnv
	}
	return witnessWidthEnv(n)
}

func witnessWidthEnv(n *NormalizedInvocation) []string {
	width := witnessChildWidth(n)
	if v, ok := lookupEnv(n.Env, "GOMAXPROCS"); ok {
		if ambient, err := strconv.Atoi(v); err == nil && ambient > 0 && ambient <= width {
			return n.Env
		}
	}
	return gotool.SetEnv(n.Env, "GOMAXPROCS", strconv.Itoa(width))
}

func incompleteObservation(pkg string, producer *stipulatorv1.ProducerIdentity, reason string) *ProcessObservation {
	wire := &stipulatorv1.Observation{}
	wire.SetProducer(producer)
	wire.SetPackage(pkg)
	wire.SetIncompleteReason(reason)
	return &ProcessObservation{Wire: wire}
}

func processIdentity(n *NormalizedInvocation, producer *stipulatorv1.ProducerIdentity, pkg string) string {
	return fmt.Sprintf("%s#%d:%s", n.Name, producer.GetProcessOrdinal(), pkg)
}

func resolvedConfig(n *NormalizedInvocation) *stipulatorv1.GoResolvedConfig {
	rc := &stipulatorv1.GoResolvedConfig{}
	rc.SetToolchain(n.Toolchain)
	rc.SetGoos(n.GOOS)
	rc.SetGoarch(n.GOARCH)
	rc.SetCgoEnabled(n.CgoEnabled)
	rc.SetGoflags(n.GOFLAGS)
	rc.SetGoexperiment(n.GOEXPERIMENT)
	rc.SetWorkspaceOn(n.WorkspaceOn)
	rc.SetRace(n.Race)
	rc.SetPlainWitness(n.PlainWitness)
	rc.SetLinkerAssignments(append([]string(nil), n.LinkerAssignments...))
	rc.SetInputFiles(inputFilePaths(n.InputFiles))
	return rc
}
