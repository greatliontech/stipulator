package cmd

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
)

type fakeCharDevice struct {
	bytes.Buffer
	charDevice bool
}

type fakeFileInfo struct {
	mode os.FileMode
}

func (f fakeFileInfo) Name() string       { return "fake" }
func (f fakeFileInfo) Size() int64        { return 0 }
func (f fakeFileInfo) Mode() os.FileMode  { return f.mode }
func (f fakeFileInfo) ModTime() time.Time { return time.Time{} }
func (f fakeFileInfo) IsDir() bool        { return false }
func (f fakeFileInfo) Sys() any           { return nil }

func (d *fakeCharDevice) Stat() (os.FileInfo, error) {
	if d.charDevice {
		return fakeFileInfo{mode: os.ModeCharDevice}, nil
	}
	return fakeFileInfo{}, nil
}

func TestPerStreamColorIndependence(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "xterm-256color")

	prob := &stipulatorv1.Problem{}
	prob.SetPath("docs/specs/x.md")
	prob.SetMessage("bad requirement")
	res := &stipulatorv1.CheckResult{}
	res.SetPassed(false)
	res.SetCompileProblems([]*stipulatorv1.Problem{prob})

	t.Run("stdout-tty-stderr-redirected", func(t *testing.T) {
		stdout := &fakeCharDevice{charDevice: true}
		stderr := &fakeCharDevice{charDevice: false}
		renderCheck(stdout, stderr, res)
		if !strings.Contains(stdout.String(), "\x1b[31mcheck: fail\x1b[0m") {
			t.Fatalf("expected ANSI red on terminal stdout, got %q", stdout.String())
		}
		if strings.Contains(stderr.String(), "\x1b[") {
			t.Fatalf("expected no ANSI escapes on redirected stderr, got %q", stderr.String())
		}
	})

	t.Run("stdout-redirected-stderr-tty", func(t *testing.T) {
		stdout := &fakeCharDevice{charDevice: false}
		stderr := &fakeCharDevice{charDevice: true}
		renderCheck(stdout, stderr, res)
		if strings.Contains(stdout.String(), "\x1b[") {
			t.Fatalf("expected no ANSI escapes on redirected stdout, got %q", stdout.String())
		}
		if !strings.Contains(stderr.String(), "\x1b[31mdocs/specs/x.md: bad requirement\x1b[0m") {
			t.Fatalf("expected ANSI red on terminal stderr, got %q", stderr.String())
		}
	})

	t.Run("no-color-disables-both", func(t *testing.T) {
		t.Setenv("NO_COLOR", "1")
		stdout := &fakeCharDevice{charDevice: true}
		stderr := &fakeCharDevice{charDevice: true}
		renderCheck(stdout, stderr, res)
		if strings.Contains(stdout.String(), "\x1b[") || strings.Contains(stderr.String(), "\x1b[") {
			t.Fatalf("expected NO_COLOR to suppress ANSI escapes on both streams: stdout=%q stderr=%q", stdout.String(), stderr.String())
		}
	})

	t.Run("term-dumb-disables-both", func(t *testing.T) {
		t.Setenv("TERM", "dumb")
		stdout := &fakeCharDevice{charDevice: true}
		stderr := &fakeCharDevice{charDevice: true}
		renderCheck(stdout, stderr, res)
		if strings.Contains(stdout.String(), "\x1b[") || strings.Contains(stderr.String(), "\x1b[") {
			t.Fatalf("expected TERM=dumb to suppress ANSI escapes on both streams: stdout=%q stderr=%q", stdout.String(), stderr.String())
		}
	})
}
