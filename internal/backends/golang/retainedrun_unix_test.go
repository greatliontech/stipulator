//go:build unix

package golang

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"testing/iotest"
	"time"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
)

func TestRetainedSignalAndEnvelopeDump(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and executes Go fixture packages")
	}
	neutralAmbient(t)
	n := retainedFixtureInvocation(t, map[string]string{"p_test.go": `package p
import ("os"; "os/exec"; "syscall"; "testing"; "time")
func TestSignal(t *testing.T){syscall.Kill(os.Getpid(),syscall.SIGTERM);time.Sleep(time.Minute)}
func TestHang(t *testing.T){for{time.Sleep(time.Second)}}
func TestPipeHold(t *testing.T){c:=exec.Command("sh","-c","sleep 30");c.Stdout=os.Stdout;c.Stderr=os.Stderr;if err:=c.Start();err!=nil{t.Fatal(err)}}
func TestStderrHold(t *testing.T){c:=exec.Command("sh","-c","sleep 30");c.Stderr=os.Stderr;if err:=c.Start();err!=nil{t.Fatal(err)}}
`})
	a := retainedBuildOK(t, n, "retained.test")
	t.Run("signal", func(t *testing.T) {
		r := retainedRuntimeFor(t, n, a, "retained.test", []string{"TestSignal"}, nil)
		result := runRetained(t.Context(), t.Context(), n, r, &stipulatorv1.ProducerIdentity{}, retainedProcessHooks{})
		if result.BinaryState == nil || result.BinaryState.Exited() || result.HarnessComplete() || result.ConversionErr != nil {
			t.Fatalf("signal: %+v", result)
		}
		status := result.BinaryState.Sys().(syscall.WaitStatus)
		if status.Signal() != syscall.SIGTERM {
			t.Fatalf("signal replaced: %v", status)
		}
	})
	for _, name := range []string{"TestHang", "TestPipeHold", "TestStderrHold"} {
		t.Run(name, func(t *testing.T) {
			r := retainedRuntimeFor(t, n, a, "retained.test", []string{name}, nil)
			ctx, cancel := context.WithTimeoutCause(t.Context(), 350*time.Millisecond, errEnvelopeExpired)
			defer cancel()
			start := time.Now()
			var reapedAt time.Time
			result := runRetained(t.Context(), ctx, n, r, &stipulatorv1.ProducerIdentity{}, retainedProcessHooks{Reaped: func(stage string, _ int, _ *os.ProcessState) {
				if stage == "binary" {
					reapedAt = time.Now()
				}
			}})
			if !result.Interrupted || result.HarnessComplete() || result.ConversionErr != nil || time.Since(start) > quitGrace+5*time.Second {
				t.Fatalf("envelope: %+v duration=%v", result, time.Since(start))
			}
			if name == "TestHang" {
				residue := runResidue(result.Stream, &boundedBuffer{}, result.BinaryErr, "").b.String()
				if !strings.Contains(residue, "SIGQUIT") || !strings.Contains(residue, "goroutine") {
					t.Fatalf("converter lost quit dump: %s", residue)
				}
			} else if reapedAt.IsZero() || !result.BinaryState.Success() {
				t.Fatalf("pipe drainage replaced the binary's real termination: reap=%v result=%+v", reapedAt, result)
			}
		})
	}
}

func TestRetainedConverterFaultsCannotComplete(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and executes Go fixture packages")
	}
	neutralAmbient(t)
	n := retainedFixtureInvocation(t, map[string]string{"p_test.go": "package p\nimport (\"testing\"; \"time\")\nfunc TestOK(t *testing.T){time.Sleep(100*time.Millisecond)}\n"})
	a := retainedBuildOK(t, n, "retained.test")
	fault := errors.New("injected output processing fault")
	for _, name := range []string{"writer", "reader", "short-write", "malformed", "read-after-terminal"} {
		t.Run(name, func(t *testing.T) {
			r := retainedRuntimeFor(t, n, a, "retained.test", nil, nil)
			retainedOutputForTest = func(src io.Reader, dst io.Writer) (io.Reader, io.Writer) {
				switch name {
				case "writer":
					dst = writerFunc(func([]byte) (int, error) { return 0, fault })
				case "reader":
					src = iotest.ErrReader(fault)
				case "read-after-terminal":
					src = io.MultiReader(src, iotest.ErrReader(fault))
				case "short-write":
					dst = writerFunc(func(p []byte) (int, error) { return len(p) - 1, nil })
				case "malformed":
					original := dst
					first := true
					dst = writerFunc(func(p []byte) (int, error) {
						if first {
							first = false
							_, _ = io.WriteString(original, "{invalid")
						}
						return len(p), nil
					})
				}
				return src, dst
			}
			defer func() { retainedOutputForTest = nil }()
			ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
			defer cancel()
			result := runRetained(ctx, ctx, n, r, &stipulatorv1.ProducerIdentity{}, retainedProcessHooks{})
			if result.HarnessComplete() {
				t.Fatalf("converter fault granted completion: %+v", result)
			}
			if result.ConversionErr == nil {
				t.Fatalf("converter fault not attributed: %+v", result)
			}
			if name == "read-after-terminal" || name == "malformed" {
				if result.BinaryState == nil || !result.BinaryState.Success() {
					t.Fatal("converter replaced binary's successful exit")
				}
			}
		})
	}
}

func TestRetainedCaptureFlushFailureCannotComplete(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and executes Go fixture packages")
	}
	if _, err := os.Stat("/dev/full"); err != nil {
		t.Skip("requires /dev/full")
	}
	neutralAmbient(t)
	n := retainedFixtureInvocation(t, map[string]string{"p_test.go": "package p\nimport (\"os\";\"testing\")\nfunc TestOK(t *testing.T){os.Getenv(\"HOME\")}\n"})
	a := retainedBuildOK(t, n, "retained.test")
	r := retainedRuntimeFor(t, n, a, "retained.test", nil, nil)
	r.Args = append(r.Args, "-test.testlogfile=/dev/full")
	result := runRetained(t.Context(), t.Context(), n, r, &stipulatorv1.ProducerIdentity{}, retainedProcessHooks{})
	if result.HarnessComplete() || result.BinaryState == nil || result.BinaryState.ExitCode() != 2 || result.HarnessTerminal != "pass" {
		t.Fatalf("flush failure completion: %+v; stream=%+v", result, result.Stream)
	}
	if !strings.Contains(result.Stream.pkgOutput.b.String(), "can't write /dev/full") {
		t.Fatal("flush error lost")
	}
}

func TestRetainedCallerCancellationEscalatesExpiredEnvelope(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and executes Go fixture packages")
	}
	neutralAmbient(t)
	n := retainedFixtureInvocation(t, map[string]string{"p_test.go": `package p
import ("os"; "os/signal"; "syscall"; "testing"; "time")
func TestWait(t *testing.T){
 quit:=make(chan os.Signal,1);signal.Notify(quit,syscall.SIGQUIT)
 if err:=os.WriteFile("ready",nil,0600);err!=nil{t.Fatal(err)}
 <-quit
 if err:=os.WriteFile("quit-seen",nil,0600);err!=nil{t.Fatal(err)}
 for{time.Sleep(time.Second)}
}

`})
	a := retainedBuildOK(t, n, "retained.test")
	r := retainedRuntimeFor(t, n, a, "retained.test", nil, nil)
	caller, cancel := context.WithCancel(t.Context())
	defer cancel()
	envelope, expire := context.WithCancelCause(caller)
	defer expire(context.Canceled)
	done := make(chan retainedRunResult, 1)
	go func() {
		done <- runRetained(caller, envelope, n, r, &stipulatorv1.ProducerIdentity{}, retainedProcessHooks{})
	}()
	await := func(name string) {
		t.Helper()
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		tick := time.NewTicker(5 * time.Millisecond)
		defer tick.Stop()
		for {
			if _, err := os.Stat(filepath.Join(n.Dir, name)); err == nil {
				return
			}
			select {
			case <-tick.C:
			case <-timer.C:
				t.Fatalf("binary never wrote %s", name)
			}
		}
	}
	await("ready")
	expire(errEnvelopeExpired)
	await("quit-seen")
	start := time.Now()
	cancel()
	select {
	case result := <-done:
		if !result.Interrupted || result.HarnessComplete() || result.BinaryState == nil || result.BinaryState.Success() || result.Stream == nil {
			t.Fatalf("escalation=%+v", result)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("caller cancellation waited for envelope grace: %v", time.Since(start))
	}
}

func TestRetainedEnvelopeEscalatesAfterOutputEOF(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and executes a SIGQUIT-ignoring fixture")
	}
	neutralAmbient(t)
	n := retainedFixtureInvocation(t, map[string]string{"p_test.go": `package p
import ("os"; "os/signal"; "syscall"; "testing"; "time")
func TestMain(m *testing.M) {
 signal.Ignore(syscall.SIGQUIT)
 if err:=os.WriteFile("ready",nil,0600);err!=nil{panic(err)}
 for {
  if _,err:=os.Stat("check-alive");err==nil {
   if err:=os.WriteFile("alive-after-eof",nil,0600);err!=nil{panic(err)}
   for { time.Sleep(time.Hour) }
  }
  time.Sleep(5*time.Millisecond)
 }
}
`})
	a := retainedBuildOK(t, n, "retained.test")
	r := retainedRuntimeFor(t, n, a, "retained.test", nil, nil)
	caller, cancel := context.WithTimeout(t.Context(), quitGrace+15*time.Second)
	defer cancel()
	ctx, expire := context.WithCancelCause(caller)
	defer expire(context.Canceled)
	input, feed := io.Pipe()
	defer input.Close()
	defer feed.Close()
	convertedEOF := make(chan struct{})
	// A real conversion EOF is independent of the parent's binary-output
	// descriptor, which stays open until the live child exits. The final
	// package event is emitted only by the converter's EOF close.
	retainedOutputForTest = func(_ io.Reader, dst io.Writer) (io.Reader, io.Writer) {
		return input, writerFunc(func(p []byte) (int, error) {
			n, err := dst.Write(p)
			if err == nil && strings.Contains(string(p), `"Action":"pass"`) {
				close(convertedEOF)
			}
			return n, err
		})
	}
	spawned := make(chan int, 1)
	done := make(chan retainedRunResult, 1)
	returned := make(chan struct{})
	go func() {
		defer close(returned)
		done <- runRetained(caller, ctx, n, r, &stipulatorv1.ProducerIdentity{}, retainedProcessHooks{Spawned: func(_ string, pid int) { spawned <- pid }})
	}()
	defer func() {
		cancel()
		_ = input.Close()
		_ = feed.Close()
		<-returned
		retainedOutputForTest = nil
	}()
	await := func(name string) {
		t.Helper()
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		tick := time.NewTicker(5 * time.Millisecond)
		defer tick.Stop()
		for {
			if _, err := os.Stat(filepath.Join(n.Dir, name)); err == nil {
				return
			}
			select {
			case <-tick.C:
			case result := <-done:
				t.Fatalf("binary ended before %s: %+v", name, result)
			case <-timer.C:
				t.Fatalf("binary never acknowledged %s", name)
			}
		}
	}
	await("ready")
	pid := <-spawned
	if _, err := io.WriteString(feed, "PASS\n"); err != nil {
		t.Fatal(err)
	}
	if err := feed.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-convertedEOF:
	case <-time.After(5 * time.Second):
		t.Fatal("owned conversion did not reach EOF")
	}
	if err := os.WriteFile(filepath.Join(n.Dir, "check-alive"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	await("alive-after-eof")
	start := time.Now()
	expire(errEnvelopeExpired)
	select {
	case result := <-done:
		elapsed := time.Since(start)
		if !result.Interrupted || result.BinaryState == nil || result.BinaryState.Success() || result.BinaryErr == nil || result.HarnessComplete() || result.ConversionErr != nil || result.HarnessTerminal != "pass" || caller.Err() != nil || elapsed < quitGrace || elapsed > quitGrace+2*time.Second {
			t.Fatalf("output EOF ended kill escalation: %+v; duration=%v", result, elapsed)
		}
		if result.BinaryState.Pid() != pid || result.Producer.GetProcessId() != int64(pid) || result.BinaryState.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
			t.Fatalf("binary facts changed: %+v", result)
		}
	case <-time.After(quitGrace + 3*time.Second):
		cancel()
		<-done
		t.Fatal("conversion EOF suppressed binary kill escalation")
	}
}
