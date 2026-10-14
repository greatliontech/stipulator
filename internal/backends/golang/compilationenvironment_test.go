package golang

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/greatliontech/gofresh/gotool"
	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/stipulate"
)

func TestGroupAnalysisUsesTheCompilationEnvironment(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness-inputs")
	neutralAmbient(t)
	dir := writeModule(t, map[string]string{
		"go.mod": "module example.com/compiler\n\ngo 1.27\n",
		"p.go":   "package compiler\nfunc F() {}\n",
	})
	analysis := gotool.SetEnv(os.Environ(), "GOMAXPROCS", "7")
	analysis = gotool.SetEnv(analysis, "COMPILER_CONFIGURATION", "analysis")
	compilation := gotool.SetEnv(analysis, "GOMAXPROCS", "1")
	compilation = gotool.SetEnv(compilation, "COMPILER_CONFIGURATION", "compilation")
	g := &captureGroup{env: analysis, witnessEnv: compilation, toolchain: runtime.Version()}
	old := engineCommandHook
	t.Cleanup(func() { engineCommandHook = old })
	seen := 0
	engineCommandHook = func(cmd *exec.Cmd) {
		seen++
		if width, _ := gotool.LookupEnv(cmd.Env, "GOMAXPROCS"); width != "1" {
			t.Errorf("analysis command width = %q; compilation uses 1", width)
		}
		if marker, _ := gotool.LookupEnv(cmd.Env, "COMPILER_CONFIGURATION"); marker != "compilation" {
			t.Errorf("analysis command used %q compiler configuration", marker)
		}
	}
	if _, err := groupEngine(t.Context(), dir, g); err != nil {
		t.Fatal(err)
	}
	if seen == 0 {
		t.Fatal("no analysis command observed")
	}
}

func TestCompilingCommandAndTestBinaryHaveTheirOwnDirectories(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness-inputs")
	neutralAmbient(t)
	dir := writeModule(t, map[string]string{
		"go.mod": "module example.com/directories\n\ngo 1.27\n",
		"p/p_test.go": `package p
import ("os"; "path/filepath"; "testing")
func TestDirectory(t *testing.T) {
    wd, err := os.Getwd()
    if err != nil { t.Fatal(err) }
    if filepath.Clean(os.Getenv("PWD")) != filepath.Clean(wd) { t.Fatalf("binary PWD=%q cwd=%q", os.Getenv("PWD"), wd) }
}
`,
	})
	realGo, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	shimDir := t.TempDir()
	source := filepath.Join(shimDir, "wrapper.go")
	program := fmt.Sprintf(`package main
import ("os"; "os/exec")
func main() {
    if len(os.Args)>1 && os.Args[1]=="test" {
        if err:=os.WriteFile(os.Getenv("CERT_PWD_LOG"), []byte(os.Getenv("PWD")), 0600); err!=nil { panic(err) }
    }
    c:=exec.Command(%q, os.Args[1:]...)
    c.Env=os.Environ(); c.Stdin=os.Stdin; c.Stdout=os.Stdout; c.Stderr=os.Stderr
    if err:=c.Run(); err!=nil { if e,ok:=err.(*exec.ExitError); ok { os.Exit(e.ExitCode()) }; panic(err) }
}
`, realGo)
	if err := os.WriteFile(source, []byte(program), 0o644); err != nil {
		t.Fatal(err)
	}
	name := "go"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	build := exec.CommandContext(t.Context(), realGo, "build", "-o", filepath.Join(shimDir, name), source)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build compiler observer: %v\n%s", err, out)
	}
	log := filepath.Join(t.TempDir(), "pwd")
	t.Setenv("CERT_PWD_LOG", log)
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./p"})
	n, err := NormalizeInvocation(t.Context(), dir, goInvocation("directories", cfg))
	if err != nil {
		t.Fatal(err)
	}
	discovered, err := DiscoverInvocation(t.Context(), n)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(log); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	health, tests, diags, _, err := ExecuteInvocation(t.Context(), n, discovered)
	if err != nil || len(tests) != 1 || tests[0].GetOutcome() != stipulatorv1.TestOutcome_TEST_OUTCOME_PASSED {
		t.Fatalf("test binary: %v %v %v %v", health, tests, diags, err)
	}
	got, err := os.ReadFile(log)
	if err != nil || strings.TrimSpace(string(got)) != n.Dir {
		t.Fatalf("compiler PWD=%q err=%v, want invocation directory %q", got, err, n.Dir)
	}
}
