package builder

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/carl-egge/ise-predictive-refaas/internal/compare"
	"github.com/carl-egge/ise-predictive-refaas/internal/domain"
	"github.com/carl-egge/ise-predictive-refaas/internal/pipeline"
)

// TestValidateHarnessOutputHappyPath compares a harness-wrapped Go response
// against an expected fixture in the canonical (paper) format, judged by the
// shared comparator under the canonical default (tolerant).
func TestValidateHarnessOutputHappyPath(t *testing.T) {
	expected := `{"statusCode": 200, "body": "{\"result\": 3}"}`
	actual := `{"response":{"statusCode":200,"headers":null,"body":"{\"result\":3}"}}`
	if ok, reason := validateHarnessOutput([]byte(expected), actual, compare.Tolerant); !ok {
		t.Errorf("matching wrapped response must pass validation, got: %s", reason)
	}

	wrongStatus := `{"response":{"statusCode":500,"body":"{\"result\":3}"}}`
	if ok, reason := validateHarnessOutput([]byte(expected), wrongStatus, compare.Tolerant); ok {
		t.Error("mismatching statusCode must fail validation")
	} else if !strings.Contains(reason, "statusCode") {
		t.Errorf("mismatch reason should name the diverging path, got: %s", reason)
	}

	// multi-key body strings must compare structurally: key order and
	// spacing differences between json.dumps and json.Marshal are irrelevant
	expectedMulti := `{"statusCode": 200, "body": "{\"a\": 1, \"b\": 2}"}`
	actualMulti := `{"response":{"statusCode":200,"body":"{\"b\":2,\"a\":1}"}}`
	if ok, reason := validateHarnessOutput([]byte(expectedMulti), actualMulti, compare.Tolerant); !ok {
		t.Errorf("body key order must not matter, got: %s", reason)
	}

	// no expectation declared (side-effect-only case) skips output validation
	if ok, _ := validateHarnessOutput(nil, `{"response":{"anything":1}}`, compare.Tolerant); !ok {
		t.Error("an empty expectation must skip output validation")
	}
}

// TestValidateHarnessOutputChecksAllSiblingKeys guards against the historical
// early-return bug where a matching nested object caused all remaining
// expected keys to be skipped (nondeterministically, due to map iteration
// order).
func TestValidateHarnessOutputChecksAllSiblingKeys(t *testing.T) {
	expected := `{"a": {"x": 1}, "b": 2}`
	actual := `{"a": {"x": 1}, "b": 3}`
	// run repeatedly: the old bug only surfaced when "a" was iterated first
	for i := 0; i < 50; i++ {
		if ok, _ := validateHarnessOutput([]byte(expected), actual, compare.Strict); ok {
			t.Fatal("mismatching sibling key must fail validation even when a nested object matches")
		}
	}
}

// TestValidateHarnessOutputModes pins the per-fixture outputMode semantics:
// shape ignores values but not types, strict catches stringified numbers,
// and the tolerant default accepts them (the canonical floci behavior).
func TestValidateHarnessOutputModes(t *testing.T) {
	expected := `{"statusCode": 200, "body": "{\"temp\": 21.5, \"city\": \"Hamburg\"}"}`
	sameShape := `{"response":{"statusCode":200,"body":"{\"temp\":3.2,\"city\":\"Berlin\"}"}}`
	if ok, reason := validateHarnessOutput([]byte(expected), sameShape, compare.ShapeOnly); !ok {
		t.Errorf("same-shape output must pass shape-only validation, got: %s", reason)
	}

	wrongType := `{"response":{"statusCode":200,"body":"{\"temp\":\"3.2\",\"city\":\"Berlin\"}"}}`
	if ok, _ := validateHarnessOutput([]byte(expected), wrongType, compare.ShapeOnly); ok {
		t.Error("a type change must fail even in shape-only mode")
	}

	// strict mode must reject the same-shape/different-values output
	if ok, _ := validateHarnessOutput([]byte(expected), sameShape, compare.Strict); ok {
		t.Error("different values must fail strict validation")
	}

	// the stringified-number class: rejected under strict, accepted under
	// the tolerant default ("3" matches 3 - the floci vectors' semantics)
	stringified := `{"response":{"statusCode":"200"}}`
	if ok, _ := validateHarnessOutput([]byte(`{"statusCode": 200}`), stringified, compare.Strict); ok {
		t.Error("string statusCode vs numeric expectation must fail strict validation")
	}
	if ok, reason := validateHarnessOutput([]byte(`{"statusCode": 200}`), stringified, compare.Tolerant); !ok {
		t.Errorf("tolerant mode must accept a stringified number, got: %s", reason)
	}
}

// writeRunnablePackage lays out a minimal buildable Go main package so
// GoPackageTester can execute it with `go run .`.
func writeRunnablePackage(t *testing.T, mainSrc string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(mainSrc), 0o644); err != nil {
		t.Fatalf("writing main.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module fnrun\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatalf("writing go.mod: %v", err)
	}
	return dir
}

// TestGoPackageTesterCollectsMismatchEvidence guards the C1 behavior: a
// failing test must surface input, expected and actual output (not just a
// count) so the align stage has something to work with.
func TestGoPackageTesterCollectsMismatchEvidence(t *testing.T) {
	dir := writeRunnablePackage(t, `package main

import "fmt"

func main() { fmt.Println(`+"`"+`{"foo": 1}`+"`"+`) }
`)
	runner := pipeline.NewRunner(context.Background(), nil, nil)
	runner.SetWorkingDir(dir)
	tester := NewGoPackageTester(map[string]interface{}{"strategy": "json"})

	req := &domain.ConversionRequest{WorkingPackage: &domain.DeploymentPackage{
		RootFile: "package main",
		TestFiles: map[string]string{
			"test/t1.json": `{"input":"{\"a\":1}","output":"{\"bar\":2}"}`,
		},
	}}

	err := tester.Apply(runner, req)
	if err == nil {
		t.Fatal("expected the test run to fail")
	}
	var te domain.TestingError
	if !errors.As(err, &te) {
		t.Fatalf("expected a TestingError, got %T: %v", err, err)
	}
	failures := te.Failures()
	if len(failures) != 1 {
		t.Fatalf("Failures() = %d entries, want 1", len(failures))
	}
	f := failures[0]
	if f.Kind != domain.TestFailureMismatch {
		t.Errorf("Kind = %q, want %q", f.Kind, domain.TestFailureMismatch)
	}
	if !strings.Contains(f.Input, `"a":1`) || !strings.Contains(f.Expected, "bar") || !strings.Contains(f.Actual, "foo") {
		t.Errorf("evidence incomplete: %+v", f)
	}
	if !strings.Contains(f.Detail, "mismatch at") {
		t.Errorf("Detail should carry the divergence path, got: %q", f.Detail)
	}
	if !strings.Contains(err.Error(), "1/1 tests failed") {
		t.Errorf("summary should name the count, got: %v", err)
	}
}

// TestGoPackageTesterRequiresWorkingDir guards [B7]: without a preceding
// goBuilder task, runner.WorkingDir() is "" and cmd.Dir would silently fall
// back to the refaas process's own CWD ("go run ." there instead of the
// translated package). Apply must fail loudly instead.
func TestGoPackageTesterRequiresWorkingDir(t *testing.T) {
	runner := pipeline.NewRunner(context.Background(), nil, nil)
	tester := NewGoPackageTester(nil)

	req := &domain.ConversionRequest{
		CurrentTask: "tester",
		WorkingPackage: &domain.DeploymentPackage{
			RootFile:  "package main",
			TestFiles: map[string]string{"test/t1.json": `{"input":"{}","output":"{}"}`},
		},
	}

	err := tester.Apply(runner, req)
	if err == nil {
		t.Fatal("expected Apply to fail when the working directory is unset")
	}
	if !strings.Contains(err.Error(), "goBuilder") {
		t.Errorf("error should name the missing goBuilder prerequisite, got: %v", err)
	}
}

// TestGoPackageTesterRecordsOutcomes guards [H1a]: each case's result is
// persisted with the classification the repair loop already produces, plus
// the comparison mode - a "shape" case compares types only and so cannot
// evidence value-level equivalence, which the analysis has to be able to see.
func TestGoPackageTesterRecordsOutcomes(t *testing.T) {
	dir := writeRunnablePackage(t, `package main

import "fmt"

func main() { fmt.Println(`+"`"+`{"foo": 1}`+"`"+`) }
`)
	buildFn(t, dir)
	runner := pipeline.NewRunner(context.Background(), nil, nil)
	runner.SetWorkingDir(dir)
	tester := NewGoPackageTester(map[string]interface{}{})

	req := &domain.ConversionRequest{
		Metrics: &domain.Metrics{TestCases: map[string]bool{}},
		WorkingPackage: &domain.DeploymentPackage{
			RootFile: "package main",
			TestFiles: map[string]string{
				// passes: the shape matches, values are ignored
				"test/t1.json": `{"name":"shape-pass","payload":{},"expectedOutput":{"foo":99},"outputMode":"shape"}`,
				// fails: value mismatch under the default tolerant mode
				"test/t2.json": `{"name":"value-fail","payload":{},"expectedOutput":{"foo":2}}`,
				// fails to parse at all
				"test/t3.json": `{not json`,
			},
		},
	}

	if err := tester.Apply(runner, req); err == nil {
		t.Fatal("expected the run to fail")
	}

	byName := map[string]domain.TestOutcome{}
	for _, o := range req.Metrics.TestOutcomes {
		byName[o.Name] = o
	}
	if len(byName) != 3 {
		t.Fatalf("expected an outcome per fixture, got %+v", req.Metrics.TestOutcomes)
	}

	if pass := byName["shape-pass"]; !pass.Passed || pass.OutputMode != "shape" {
		t.Errorf("shape case outcome = %+v, want passed with its mode recorded", pass)
	}
	if fail := byName["value-fail"]; fail.Passed || fail.Kind != domain.TestFailureMismatch {
		t.Errorf("value case outcome = %+v, want a mismatch failure", fail)
	} else if fail.OutputMode != "tolerant" {
		t.Errorf("default mode should be recorded explicitly, got %q", fail.OutputMode)
	} else if fail.Detail == "" {
		t.Error("a mismatch should carry the divergence detail")
	}
	if broken := byName["test/t3.json"]; broken.Kind != domain.TestFailureFixture {
		t.Errorf("unparseable fixture outcome = %+v, want an invalid-fixture kind", broken)
	}
	for _, o := range req.Metrics.TestOutcomes {
		if o.Route != routeGoTester {
			t.Errorf("outcome %q not labelled with its route: %+v", o.Name, o)
		}
	}
	// the legacy compact view stays correct
	if !req.Metrics.TestCases["shape-pass"] || req.Metrics.TestCases["value-fail"] {
		t.Errorf("legacy TestCases map out of sync: %v", req.Metrics.TestCases)
	}
}

// TestGoPackageTesterCollectsCrashEvidence verifies a non-zero exit is
// reported as an execution error with the captured stderr.
func TestGoPackageTesterCollectsCrashEvidence(t *testing.T) {
	dir := writeRunnablePackage(t, `package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "boom: nil map write")
	os.Exit(3)
}
`)
	runner := pipeline.NewRunner(context.Background(), nil, nil)
	runner.SetWorkingDir(dir)
	tester := NewGoPackageTester(map[string]interface{}{"strategy": "json"})

	req := &domain.ConversionRequest{WorkingPackage: &domain.DeploymentPackage{
		RootFile: "package main",
		TestFiles: map[string]string{
			"test/t1.json": `{"input":"{}","output":"{\"ok\":true}"}`,
		},
	}}

	err := tester.Apply(runner, req)
	var te domain.TestingError
	if !errors.As(err, &te) || len(te.Failures()) != 1 {
		t.Fatalf("expected a TestingError with one failure, got %v", err)
	}
	f := te.Failures()[0]
	if f.Kind != domain.TestFailureError {
		t.Errorf("Kind = %q, want %q", f.Kind, domain.TestFailureError)
	}
	if !strings.Contains(f.Stderr, "boom") {
		t.Errorf("Stderr should carry the crash output, got: %q", f.Stderr)
	}
}

// buildFn compiles the package in dir to ./fn, the artifact goBuilder
// normally produces, so tests can exercise the direct-binary execution path.
func buildFn(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("go", "build", "-o", "fn", ".")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build -o fn: %v\n%s", err, out)
	}
}

// TestGoPackageTesterIsolatesAWSEnv is the end-to-end half of [C11]: the
// program under test must actually observe the emulator endpoint and dummy
// credentials, with the host's real AWS variables gone. The fixture asserts
// on what the process itself reports seeing.
func TestGoPackageTesterIsolatesAWSEnv(t *testing.T) {
	dir := writeRunnablePackage(t, `package main

import (
	"encoding/json"
	"fmt"
	"os"
)

func main() {
	out, _ := json.Marshal(map[string]string{
		"endpoint": os.Getenv("AWS_ENDPOINT_URL"),
		"key":      os.Getenv("AWS_ACCESS_KEY_ID"),
		"profile":  os.Getenv("AWS_PROFILE"),
	})
	fmt.Println(string(out))
}
`)
	buildFn(t, dir)

	// a real credential in the host environment of this test process
	t.Setenv("AWS_PROFILE", "production")
	t.Setenv("AWS_ACCESS_KEY_ID", "AKIAREALCREDENTIAL")

	runner := pipeline.NewRunner(context.Background(), nil, nil)
	runner.SetWorkingDir(dir)
	tester := NewGoPackageTester(map[string]interface{}{})

	req := &domain.ConversionRequest{
		CurrentTask: "tester",
		WorkingPackage: &domain.DeploymentPackage{
			RootFile: "package main",
			TestFiles: map[string]string{
				// the expectation *is* the assertion: harness endpoint and
				// dummy key present, host profile gone
				"test/t1.json": `{"payload":{},"expectedOutput":{"endpoint":"http://localhost:4566","key":"test","profile":""}}`,
			},
		},
	}

	if err := tester.Apply(runner, req); err != nil {
		t.Fatalf("the translated program did not observe the isolated AWS environment: %v", err)
	}
}

// TestGoPackageTesterTimesOutHangingPrograms guards the F1 behavior: a
// translated infinite loop must fail its test with a timeout kind instead
// of hanging the single worker goroutine indefinitely. The package is
// pre-built to ./fn, which also exercises the G1 direct-binary path (the
// timeout then kills the program itself, not just a `go run` parent).
func TestGoPackageTesterTimesOutHangingPrograms(t *testing.T) {
	dir := writeRunnablePackage(t, `package main

import "time"

func main() { time.Sleep(30 * time.Second) }
`)
	buildFn(t, dir)
	runner := pipeline.NewRunner(context.Background(), nil, nil)
	runner.SetWorkingDir(dir)
	tester := NewGoPackageTester(map[string]interface{}{"strategy": "json", "test_timeout": "3s"})

	req := &domain.ConversionRequest{WorkingPackage: &domain.DeploymentPackage{
		RootFile: "package main",
		TestFiles: map[string]string{
			"test/t1.json": `{"input":"{}","output":"{\"ok\":true}"}`,
		},
	}}

	err := tester.Apply(runner, req)
	var te domain.TestingError
	if !errors.As(err, &te) || len(te.Failures()) != 1 {
		t.Fatalf("expected a TestingError with one failure, got %v", err)
	}
	f := te.Failures()[0]
	if f.Kind != domain.TestFailureTimeout {
		t.Errorf("Kind = %q, want %q", f.Kind, domain.TestFailureTimeout)
	}
	if !strings.Contains(f.Stderr, "time limit") {
		t.Errorf("Stderr should explain the timeout, got: %q", f.Stderr)
	}
}

// TestRunBuildCommandsTimeout verifies a hanging build command is killed
// with a descriptive error instead of blocking the worker.
func TestRunBuildCommandsTimeout(t *testing.T) {
	b := &GolangBuilder{buildTimeout: 300 * time.Millisecond}
	_, err := b.runBuildCommands(context.Background(), t.TempDir(), "sleep 5")
	if err == nil {
		t.Fatal("expected a timeout error")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("error should mention the timeout, got: %v", err)
	}
}

// TestValidateHarnessOutputDoesNotPanicOnTypeMismatch guards against
// unchecked type assertions: a scalar "response", or expected/actual leaves
// of different JSON types, must produce a mismatch, not a panic that aborts
// the whole conversion run.
func TestValidateHarnessOutputDoesNotPanicOnTypeMismatch(t *testing.T) {
	// handler returned a scalar where an object was expected
	if ok, _ := validateHarnessOutput([]byte(`{"statusCode": 200}`), `{"response":"just a string"}`, compare.Strict); ok {
		t.Error("scalar response vs object expectation must fail validation")
	}
	// number vs string leaf (the stringified-number translation bug)
	if ok, _ := validateHarnessOutput([]byte(`{"statusCode": 200}`), `{"response":{"statusCode":"200"}}`, compare.Strict); ok {
		t.Error("string statusCode vs numeric expectation must fail validation")
	}
	// string vs number leaf (reverse direction)
	if ok, _ := validateHarnessOutput([]byte(`{"statusCode": "200"}`), `{"response":{"statusCode":200}}`, compare.Strict); ok {
		t.Error("numeric statusCode vs string expectation must fail validation")
	}
	// harness-reported handler error
	if ok, reason := validateHarnessOutput([]byte(`{"statusCode": 200}`), `{"error":"runtime blew up"}`, compare.Strict); ok {
		t.Error("a harness error envelope must fail validation")
	} else if !strings.Contains(reason, "handler returned an error") {
		t.Errorf("reason should surface the handler error, got: %s", reason)
	}
}

// TestGoPackageTesterReadsRichFixtures guards the C12 unification: the
// goTester must consume the canonical (Floci-shaped) fixture format directly
// - payload as the event, expectedOutput judged per outputMode, and declared
// setup/sideEffects tolerated (ignored with a warning) rather than breaking
// the run. Unknown fields like an external "provenance" block are ignored.
func TestGoPackageTesterReadsRichFixtures(t *testing.T) {
	// Echoes the harness envelope a translated function would produce.
	dir := writeRunnablePackage(t, `package main

import "fmt"

func main() { fmt.Println(`+"`"+`{"response": {"statusCode": 200, "body": "{\"result\": 3}"}}`+"`"+`) }
`)
	runner := pipeline.NewRunner(context.Background(), nil, nil)
	runner.SetWorkingDir(dir)
	tester := NewGoPackageTester(nil)

	req := &domain.ConversionRequest{WorkingPackage: &domain.DeploymentPackage{
		RootFile: "package main",
		TestFiles: map[string]string{
			"test/t1.json": `{
				"name": "happy-path",
				"description": "rich fixture consumed by goTester",
				"payload": {"num1": 1, "num2": 2},
				"expectedOutput": {"statusCode": 200, "body": "{\"result\": 3}"},
				"outputMode": "strict",
				"setup": [],
				"sideEffects": [{"type": "s3.objectExists", "bucket": "b", "key": "k"}],
				"provenance": {"method": "mined", "output_source": "golden"}
			}`,
			"test/t2.json": `{"name": "effects-only", "payload": {"x": 1}}`,
		},
	}}

	if err := tester.Apply(runner, req); err != nil {
		t.Fatalf("rich fixtures must validate through goTester, got: %v", err)
	}
}

// TestGoPackageTesterRichFixtureMismatch verifies a rich fixture's
// expectedOutput mismatch produces the usual evidence, with the payload as
// the reported input.
func TestGoPackageTesterRichFixtureMismatch(t *testing.T) {
	dir := writeRunnablePackage(t, `package main

import "fmt"

func main() { fmt.Println(`+"`"+`{"response": {"statusCode": 500}}`+"`"+`) }
`)
	runner := pipeline.NewRunner(context.Background(), nil, nil)
	runner.SetWorkingDir(dir)
	tester := NewGoPackageTester(nil)

	req := &domain.ConversionRequest{WorkingPackage: &domain.DeploymentPackage{
		RootFile: "package main",
		TestFiles: map[string]string{
			"test/t1.json": `{"name": "wrong-status", "payload": {"a": 1}, "expectedOutput": {"statusCode": 200}}`,
		},
	}}

	err := tester.Apply(runner, req)
	var te domain.TestingError
	if !errors.As(err, &te) || len(te.Failures()) != 1 {
		t.Fatalf("expected a TestingError with one failure, got %v", err)
	}
	f := te.Failures()[0]
	if f.Name != "wrong-status" {
		t.Errorf("Name = %q, want the fixture's own name", f.Name)
	}
	if !strings.Contains(f.Input, `"a": 1`) || !strings.Contains(f.Expected, "200") {
		t.Errorf("evidence should carry payload and expected output: %+v", f)
	}
	if !strings.Contains(f.Detail, "mismatch at") {
		t.Errorf("Detail should carry the divergence path, got: %q", f.Detail)
	}
}

// TestGoPackageTesterMergesEnv verifies the A5 env semantics survived the
// schema unification: package-level .env entries reach the test process and
// a fixture's own "env" entries override them (exec.Cmd keeps the last
// duplicate key).
func TestGoPackageTesterMergesEnv(t *testing.T) {
	dir := writeRunnablePackage(t, `package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Printf(`+"`"+`{"response": {"shared": "%s", "extra": "%s"}}`+"`"+`, os.Getenv("SHARED"), os.Getenv("EXTRA"))
}
`)
	runner := pipeline.NewRunner(context.Background(), nil, nil)
	runner.SetWorkingDir(dir)
	tester := NewGoPackageTester(nil)

	req := &domain.ConversionRequest{WorkingPackage: &domain.DeploymentPackage{
		RootFile: "package main",
		Env:      []string{"SHARED=pkg", "EXTRA=1"},
		TestFiles: map[string]string{
			"test/t1.json": `{"input":"{}","output":"{\"shared\":\"test\",\"extra\":\"1\"}","env":["SHARED=test"]}`,
		},
	}}

	if err := tester.Apply(runner, req); err != nil {
		t.Fatalf("per-test env override must win over the package env, got: %v", err)
	}
}

// --- [A18] stdout is the harness's channel, not the function's ---------------

// writeHarnessPackage lays out a buildable package wired to the *real*
// embedded harness (test_handler.txt), so these tests exercise the marker
// contract end to end rather than a hand-written stand-in. The harness only
// calls handle and marshals whatever it returns, so a local response type is
// enough and the aws-lambda-go dependency is not needed here.
func writeHarnessPackage(t *testing.T, mainSrc string) string {
	t.Helper()
	dir := writeRunnablePackage(t, mainSrc)
	if err := os.WriteFile(filepath.Join(dir, "test_handler.go"), []byte(goTestHandler), 0o644); err != nil {
		t.Fatalf("writing test_handler.go: %v", err)
	}
	return dir
}

// TestHarnessEnvelopeExtraction pins the two layers that keep function output
// out of the response: the marker, and the balanced-object scan behind it.
func TestHarnessEnvelopeExtraction(t *testing.T) {
	envelope := `{"response":{"statusCode":200,"body":"{\"result\":3}"}}`
	cases := []struct {
		name   string
		stdout string
		want   string
	}{
		{
			name:   "marker separates function output from the envelope",
			stdout: "2026/08/07 15:35:19 called with event: {\"a\":1}" + harnessOutputMarker + envelope,
			want:   envelope,
		},
		{
			name:   "bare envelope without a marker is returned unchanged",
			stdout: envelope,
			want:   envelope,
		},
		{
			name:   "no marker: the last top-level object wins over an echoed event",
			stdout: `{"httpMethod":"GET"}` + envelope,
			want:   envelope,
		},
		{
			name:   "output printed after the envelope is dropped",
			stdout: harnessOutputMarker + envelope + "goroutine finished",
			want:   envelope,
		},
		{
			// the envelope's Body is a JSON-encoded *string* full of braces;
			// a naive depth count would end the object at the first inner "}"
			name:   "braces inside a JSON string body do not confuse the scan",
			stdout: "noise " + `{"response":{"body":"{\"nested\":{\"deep\":1}}"}}`,
			want:   `{"response":{"body":"{\"nested\":{\"deep\":1}}"}}`,
		},
		{
			name:   "output with no JSON at all is passed through for the text fallback",
			stdout: "panic: something went wrong",
			want:   "panic: something went wrong",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := harnessEnvelope(tc.stdout); got != tc.want {
				t.Errorf("harnessEnvelope()\n got: %s\nwant: %s", got, tc.want)
			}
		})
	}
}

// TestValidateHarnessOutputIgnoresFunctionStdout reproduces the pf13 failure
// from run-20260807-132133: the response was byte-correct and the case still
// failed, because a log line shared the stream with the envelope and the
// comparison fell back to substring containment.
func TestValidateHarnessOutputIgnoresFunctionStdout(t *testing.T) {
	expected := `{"statusCode": 200, "body": "\"Welcome to the Low Complexity Lambda Function!\""}`
	polluted := `2026/08/07 15:35:19 Function handle called with event: {"httpMethod":"GET"}` +
		harnessOutputMarker +
		`{"response":{"statusCode":200,"headers":null,"body":"\"Welcome to the Low Complexity Lambda Function!\""}}`

	if ok, reason := validateHarnessOutput([]byte(expected), polluted, compare.Tolerant); !ok {
		t.Errorf("a correct response must pass despite the function's own stdout output, got: %s", reason)
	}

	// the guard must not swallow real divergences hidden behind the noise
	wrong := `2026/08/07 15:35:19 chatter` + harnessOutputMarker +
		`{"response":{"statusCode":500,"body":"\"Welcome to the Low Complexity Lambda Function!\""}}`
	if ok, reason := validateHarnessOutput([]byte(expected), wrong, compare.Tolerant); ok {
		t.Error("a genuine mismatch must still fail when the function also printed")
	} else if !strings.Contains(reason, "statusCode") {
		t.Errorf("mismatch reason should name the diverging path, got: %s", reason)
	}
}

// TestGoPackageTesterToleratesStdoutLoggingFunction is the end-to-end form:
// pf13's exact pattern - a package-level logger bound to os.Stdout, which is
// initialized before main runs and so cannot be redirected by main - must
// still produce a passing test.
func TestGoPackageTesterToleratesStdoutLoggingFunction(t *testing.T) {
	dir := writeHarnessPackage(t, `package main

import (
	"context"
	"encoding/json"
	"log"
	"os"
)

// initialized before main(): this logger holds the real stdout handle
var logger = log.New(os.Stdout, "", log.LstdFlags)

type response struct {
	StatusCode int    `+"`"+`json:"statusCode"`+"`"+`
	Body       string `+"`"+`json:"body"`+"`"+`
}

func handle(ctx context.Context, event json.RawMessage) (response, error) {
	logger.Printf("Function handle called with event: %s", string(event))
	return response{StatusCode: 200, Body: "\"Welcome\""}, nil
}
`)
	buildFn(t, dir)
	runner := pipeline.NewRunner(context.Background(), nil, nil)
	runner.SetWorkingDir(dir)
	tester := NewGoPackageTester(map[string]interface{}{})

	req := &domain.ConversionRequest{
		Metrics: &domain.Metrics{TestCases: map[string]bool{}},
		WorkingPackage: &domain.DeploymentPackage{
			RootFile: "package main",
			TestFiles: map[string]string{
				"test/t1.json": `{"name":"t1","payload":{"httpMethod":"GET"},"expectedOutput":{"statusCode":200,"body":"\"Welcome\""}}`,
			},
		},
	}

	if err := tester.Apply(runner, req); err != nil {
		t.Fatalf("a correct handler that logs to stdout must pass, got: %v", err)
	}
	if !req.Metrics.TestCases["t1"] {
		t.Errorf("outcome not recorded as passing: %+v", req.Metrics.TestOutcomes)
	}
}

// TestHarnessKeepsStdoutClean checks the harness's first layer directly: a
// function using fmt.Print* (which resolves os.Stdout at call time) has its
// output moved to stderr, so it stays available as failure evidence without
// ever reaching the response channel.
func TestHarnessKeepsStdoutClean(t *testing.T) {
	dir := writeHarnessPackage(t, `package main

import (
	"context"
	"encoding/json"
	"fmt"
)

type response struct {
	StatusCode int `+"`"+`json:"statusCode"`+"`"+`
}

func handle(ctx context.Context, event json.RawMessage) (response, error) {
	fmt.Println("progress chatter from the function")
	return response{StatusCode: 200}, nil
}
`)
	buildFn(t, dir)

	cmd := exec.Command(filepath.Join(dir, "fn"))
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader("{}")
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("running the harness: %v (stderr: %s)", err, stderr.String())
	}

	if strings.Contains(stdout.String(), "progress chatter") {
		t.Errorf("function output must not reach stdout, got: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "progress chatter") {
		t.Errorf("function output must be preserved on stderr as evidence, got: %q", stderr.String())
	}
	if !strings.Contains(stdout.String(), harnessOutputMarker) {
		t.Errorf("harness must mark its envelope, got: %q", stdout.String())
	}
	if !json.Valid([]byte(harnessEnvelope(stdout.String()))) {
		t.Errorf("extracted envelope must be valid JSON, got: %q", stdout.String())
	}
}

// TestGoPackageTesterOutcomesDescribeLastRound is the end-to-end form of
// [A19]: the stage is re-entered after a recovery hop, with the working
// package rewritten in between. What it records must be the state of the code
// that finally ran, not the union of every attempt. This mirrors the pf7 case
// in run-20260807-132133, which archived 9 passed / 10 outcomes for a
// five-fixture function that in truth ended at 5/5.
func TestGoPackageTesterOutcomesDescribeLastRound(t *testing.T) {
	const broken = `package main

import "fmt"

func main() { fmt.Println(` + "`" + `{"response":{"statusCode":500}}` + "`" + `) }
`
	const repaired = `package main

import "fmt"

func main() { fmt.Println(` + "`" + `{"response":{"statusCode":200}}` + "`" + `) }
`

	dir := writeRunnablePackage(t, broken)
	buildFn(t, dir)
	runner := pipeline.NewRunner(context.Background(), nil, nil)
	runner.SetWorkingDir(dir)
	tester := NewGoPackageTester(map[string]interface{}{})

	req := &domain.ConversionRequest{
		Metrics: &domain.Metrics{TestCases: map[string]bool{}},
		WorkingPackage: &domain.DeploymentPackage{
			RootFile: "package main",
			TestFiles: map[string]string{
				"test/t1.json": `{"name":"t1","payload":{},"expectedOutput":{"statusCode":200}}`,
				"test/t2.json": `{"name":"t2","payload":{},"expectedOutput":{"statusCode":200}}`,
			},
		},
	}

	// round 1: both fixtures fail
	if err := tester.Apply(runner, req); err == nil {
		t.Fatal("the broken package should fail its fixtures")
	}
	if len(req.Metrics.TestOutcomes) != 2 {
		t.Fatalf("round 1 should record one outcome per fixture, got %+v", req.Metrics.TestOutcomes)
	}

	// a recovery stage repairs the code, then the pipeline re-enters this task
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(repaired), 0o644); err != nil {
		t.Fatalf("rewriting main.go: %v", err)
	}
	buildFn(t, dir)

	// round 2: both pass
	if err := tester.Apply(runner, req); err != nil {
		t.Fatalf("the repaired package should pass, got: %v", err)
	}

	if len(req.Metrics.TestOutcomes) != 2 {
		t.Fatalf("outcomes must describe the last round only, got %d entries: %+v",
			len(req.Metrics.TestOutcomes), req.Metrics.TestOutcomes)
	}
	for _, o := range req.Metrics.TestOutcomes {
		if !o.Passed {
			t.Errorf("outcome %q still carries the pre-repair failure: %+v", o.Name, o)
		}
	}
	if !req.Metrics.TestCases["t1"] || !req.Metrics.TestCases["t2"] {
		t.Errorf("legacy TestCases map out of sync with the last round: %v", req.Metrics.TestCases)
	}
	if req.Metrics.TestError != 0 {
		t.Errorf("TestError should describe the last round too, got %d", req.Metrics.TestError)
	}
}
