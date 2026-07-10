package builder

import (
	"context"
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
