package witnesscache

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/gotool"
	"github.com/greatliontech/gofresh/runtimeinput"
	"github.com/greatliontech/stipulator/stipulate"
)

// TestHistoricalObservationProofReadableNotReusable separates the store's
// structural admission from the engine's source-bound proof admission. Only the
// proof strategy differs between the deciding historical and current records.
func TestHistoricalObservationProofReadableNotReusable(t *testing.T) {
	if testing.Short() {
		t.Skip("captures a real execution and checks source-bound observation evidence")
	}
	stipulate.Covers(t, "REQ-evidence-witness-cache-format-validation", "REQ-evidence-witness-cache-format-proof-and-outcomes")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	for name, content := range map[string]string{
		"go.mod": "module example.com/historicalproof\n\ngo 1.26\n",
		"proof_test.go": `package historicalproof
import ("os"; "testing")
func TestObserved(t *testing.T) {
	if os.Getenv("WITNESS_PROOF_VALUE") != "observed" { t.Fatal("unexpected input") }
}
`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	env, err := gotool.EnvForCommand(os.Environ(), dir)
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{"GOWORK": "off", "GOPACKAGESDRIVER": "off", "WITNESS_PROOF_VALUE": "observed"} {
		env = gotool.SetEnv(env, key, value)
	}
	bin := filepath.Join(t.TempDir(), "proof.test")
	build := exec.CommandContext(t.Context(), "go", "test", "-c", "-o", bin, ".")
	build.Dir, build.Env = dir, env
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build fixture: %v\n%s", err, output)
	}
	engine, err := gofresh.New(gofresh.WithDir(dir), gofresh.WithEnv(env...))
	if err != nil {
		t.Fatal(err)
	}
	subject := gofresh.Subject{Package: "example.com/historicalproof", Symbol: "TestObserved"}
	producer, err := engine.NewView(t.Context(), []gofresh.Subject{subject}, dir)
	if err != nil {
		t.Fatal(err)
	}
	frame := runtimeinput.CaptureProducerFrame(t.Context(), dir, dir, runtimeinput.FrameOptions{})
	support, err := producer.PrepareOutcomeSupport(t.Context(), frame, "execution")
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, err := producer.CaptureObserved(t.Context(), subject)
	if err != nil {
		t.Fatal(err)
	}
	if !fingerprint.ObservationProof.Observable || fingerprint.PurityAssertion != "" {
		t.Fatalf("fixture needs positive observation proof without purity: %+v", fingerprint)
	}
	log := filepath.Join(t.TempDir(), "test.log")
	run := exec.CommandContext(t.Context(), bin, "-test.v", "-test.run=^TestObserved$", "-test.testlogfile="+log)
	run.Dir, run.Env = dir, env
	if output, err := run.CombinedOutput(); err != nil || !strings.Contains(string(output), "--- PASS: TestObserved") {
		t.Fatalf("execute fixture: %v\n%s", err, output)
	}
	receipt, err := frame.Completion("execution", env, "")
	if err != nil {
		t.Fatal(err)
	}
	observation, reason, err := frame.Observe(t.Context(), log, runtimeinput.ProducerIngest{Identity: "execution", Env: env, Completion: receipt, Outcome: support})
	if err != nil || reason != "" {
		t.Fatalf("observe completed fixture: %q %v", reason, err)
	}
	fingerprint, err = producer.AttachObservation(subject, fingerprint, observation)
	if err != nil {
		t.Fatal(err)
	}
	if err := producer.Validate(t.Context()); err != nil {
		t.Fatal(err)
	}
	manifest, err := base64.RawURLEncoding.DecodeString(fingerprint.RuntimeInputs)
	if err != nil || !bytes.HasPrefix(manifest, []byte(`{"v":4,`)) {
		t.Fatalf("fixture needs the supported native v4 manifest: %s (%v)", manifest, err)
	}
	current, err := engine.NewView(t.Context(), []gofresh.Subject{subject}, dir)
	if err != nil {
		t.Fatal(err)
	}
	check := func(label string, fp gofresh.Fingerprint, want gofresh.Status) {
		t.Helper()
		verdict, err := current.CheckObserved(t.Context(), fp, subject)
		if err != nil || verdict.Status != want {
			t.Fatalf("%s native judgment = %+v (%v), want %s", label, verdict, err, want)
		}
	}
	check("current positive control", fingerprint, gofresh.Valid)
	withoutProof := fingerprint
	withoutProof.ObservationAssertion, withoutProof.ObservationProof = "", gofresh.ObservationProof{}
	check("ordinary unasserted control", withoutProof, gofresh.Unverifiable)
	historical := fingerprint
	historical.ObservationProof.Strategy = "gofresh/observation-rta@43"
	if historical.ObservationProof.Strategy == fingerprint.ObservationProof.Strategy {
		t.Fatal("historical strategy must differ from the producing strategy")
	}
	check("historical proof", historical, gofresh.Unverifiable)

	store, err := StoreDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	installed := make(map[string][]byte)
	for _, fp := range []gofresh.Fingerprint{fingerprint, historical} {
		data, err := json.Marshal(fp)
		if err != nil {
			t.Fatal(err)
		}
		var decoded gofresh.Fingerprint
		if err := json.Unmarshal(data, &decoded); err != nil || decoded != fp {
			t.Fatalf("native codec changed historical evidence: %+v (%v)", decoded, err)
		}
		reencoded, err := json.Marshal(decoded)
		if err != nil || !bytes.Equal(data, reencoded) {
			t.Fatalf("native codec changed canonical bytes: %s (%v)", reencoded, err)
		}
		rec := Record{Group: "historical-proof", Package: subject.Package, Test: subject.Symbol, Fingerprint: decoded,
			Outcomes: map[string]string{subject.Package + "." + subject.Symbol: "passed"}}
		if err := Install(t.Context(), dir, rec); err != nil {
			t.Fatal(err)
		}
		name, err := fileName(rec)
		if err != nil {
			t.Fatal(err)
		}
		data, err = os.ReadFile(filepath.Join(store, name))
		if err != nil {
			t.Fatal(err)
		}
		decodedRecord, _, ok := decodeRecord(name, data)
		if !ok || !reflect.DeepEqual(decodedRecord, rec) {
			t.Fatalf("supported envelope and content-derived name did not round-trip: %+v", decodedRecord)
		}
		installed[name] = data
	}
	// This malformed proof still has canonical native encoding and a matching
	// filename; rejection must come from structural proof validation, not naming.
	malformed := Record{Group: "malformed-proof", Package: subject.Package, Test: subject.Symbol, Fingerprint: fingerprint,
		Outcomes: map[string]string{subject.Package + "." + subject.Symbol: "passed"}}
	malformed.Fingerprint.ObservationProof.Evidence = "not-a-digest"
	if err := Install(t.Context(), dir, malformed); err != nil {
		t.Fatal(err)
	}
	loaded := Load(t.Context(), dir).Records
	if len(loaded) != 2 {
		t.Fatalf("loaded %d records, want current and historical siblings, excluding malformed proof", len(loaded))
	}
	seen := make(map[string]bool)
	for _, rec := range loaded {
		want, status := fingerprint, gofresh.Valid
		if rec.Fingerprint.ObservationProof.Strategy == historical.ObservationProof.Strategy {
			want, status = historical, gofresh.Unverifiable
		}
		if rec.Group != "historical-proof" || rec.Fingerprint != want || rec.Outcomes[rec.Key()] != "passed" {
			t.Fatalf("load changed producing evidence or admitted malformed sibling: %+v", rec)
		}
		seen[rec.Fingerprint.ObservationProof.Strategy] = true
		check("loaded record", rec.Fingerprint, status)
	}
	if len(seen) != 2 {
		t.Fatalf("load lost a strategy variant: %v", seen)
	}
	for name, before := range installed {
		after, err := os.ReadFile(filepath.Join(store, name))
		if err != nil || !bytes.Equal(before, after) {
			t.Fatalf("loading/checking rewrote historical evidence in %s: %v", name, err)
		}
	}
}
