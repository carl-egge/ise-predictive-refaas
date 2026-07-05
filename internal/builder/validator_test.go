package builder

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/carl-egge/ise-predictive-refaas/internal/domain"
	"github.com/carl-egge/ise-predictive-refaas/internal/pipeline"
)

// TestSimilarityValidationDirection guards against the inverted comparison
// where identical output failed validation and disjoint output passed
// (validate used to return sim < threshold instead of sim >= threshold).
func TestSimilarityValidationDirection(t *testing.T) {
	v := SimilarityValidation{}

	if !v.validate("hello world", "hello world") {
		t.Error("identical strings must pass validation")
	}
	if v.validate(`{"totally":"different"}`, "expected output") {
		t.Error("disjoint strings must fail validation")
	}
	if !v.validateUndeterministic("hello world", "hello world") {
		t.Error("identical strings must pass undeterministic validation")
	}
}

// TestJsonAwareValidateHappyPath compares a harness-wrapped Go response
// against an expected fixture in the canonical (paper) format.
func TestJsonAwareValidateHappyPath(t *testing.T) {
	v := MakeAwareSimilarityValidation(0.85)

	expected := `{"statusCode": 200, "body": "{\"result\": 3}"}`
	actual := `{"response":{"statusCode":200,"headers":null,"body":"{\"result\":3}"}}`
	if !v.validate(actual, expected) {
		t.Error("matching wrapped response must pass validation")
	}

	wrongStatus := `{"response":{"statusCode":500,"body":"{\"result\":3}"}}`
	if v.validate(wrongStatus, expected) {
		t.Error("mismatching statusCode must fail validation")
	}
}

// TestJsonAwareValidateChecksAllSiblingKeys guards against the early-return
// bug where a matching nested object caused all remaining expected keys to be
// skipped (nondeterministically, due to map iteration order).
func TestJsonAwareValidateChecksAllSiblingKeys(t *testing.T) {
	v := MakeAwareSimilarityValidation(0.85)

	expected := `{"a": {"x": 1}, "b": 2}`
	actual := `{"a": {"x": 1}, "b": 3}`
	// run repeatedly: the old bug only surfaced when "a" was iterated first
	for i := 0; i < 50; i++ {
		if v.validate(actual, expected) {
			t.Fatal("mismatching sibling key must fail validation even when a nested object matches")
		}
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

// TestJsonAwareValidateDoesNotPanicOnTypeMismatch guards against unchecked
// type assertions: a scalar "response", or expected/actual leaves of
// different JSON types, must produce a mismatch, not a panic that aborts the
// whole conversion run.
func TestJsonAwareValidateDoesNotPanicOnTypeMismatch(t *testing.T) {
	v := MakeAwareSimilarityValidation(0.85)

	// handler returned a scalar where an object was expected
	if v.validate(`{"response":"just a string"}`, `{"statusCode": 200}`) {
		t.Error("scalar response vs object expectation must fail validation")
	}
	// number vs string leaf
	if v.validate(`{"response":{"statusCode":"200"}}`, `{"statusCode": 200}`) {
		t.Error("string statusCode vs numeric expectation must fail validation")
	}
	// string vs number leaf (reverse direction)
	if v.validate(`{"response":{"statusCode":200}}`, `{"statusCode": "200"}`) {
		t.Error("numeric statusCode vs string expectation must fail validation")
	}
}
