package builder

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/adrg/strutil"
	"github.com/adrg/strutil/metrics"
	"github.com/carl-egge/ise-predictive-refaas/internal/compare"
	"github.com/carl-egge/ise-predictive-refaas/internal/domain"
	"github.com/carl-egge/ise-predictive-refaas/internal/pipeline"
	log "github.com/sirupsen/logrus"
)

// defaultTestTimeout bounds a single test-case run of the translated
// program. Generous enough for `go run`'s cached rebuild plus a network
// call, but a translated infinite loop no longer hangs the single worker.
const defaultTestTimeout = 30 * time.Second

// GoPackageTester runs the package in the working directory and validates its
// stdout against expected test outputs.
type GoPackageTester struct {
	validator ValidationStrategy
	// testTimeout bounds each individual test-case run (task_args
	// "test_timeout", default defaultTestTimeout).
	testTimeout time.Duration
}

func init() {
	pipeline.RegisterConverterFactory("goTester", NewGoPackageTester)
}

// NewGoPackageTester constructs a tester with an optional validation strategy
// and per-test timeout from args.
func NewGoPackageTester(args map[string]interface{}) pipeline.Converter {
	var validator ValidationStrategy
	if kind, ok := args["strategy"].(string); ok {
		switch kind {
		case "json":
			validator = NewJSONStructureValidation()
		default:
			validator = &SimilarityValidation{}
		}
	}
	return &GoPackageTester{
		validator:   validator,
		testTimeout: parseTimeout(args, "test_timeout", defaultTestTimeout),
	}
}

// Apply builds/runs tests in the runner's working directory and updates the
// request's metrics and error state.
func (cc *GoPackageTester) Apply(runner *pipeline.Runner, request *domain.ConversionRequest) error {
	if request.WorkingPackage == nil {
		log.Errorf("missing working package for %s", request.Id)
		return fmt.Errorf("the working package is required")
	}

	if request.SourcePackage != nil && (len(request.SourcePackage.TestFiles)) > len(request.WorkingPackage.TestFiles) {
		request.WorkingPackage.TestFiles = make(map[string]string)
		maps.Copy(request.WorkingPackage.TestFiles, request.SourcePackage.TestFiles)
		log.Debugf("Recoverting WP Tests")
	}

	startTime := time.Now()
	ctx := runner
	failures := make([]domain.TestFailure, 0)
	log.Debugf("Running GoPackageTester with %d tests", len(request.WorkingPackage.TestFiles))
	for testFile, err := range maps.Collect(request.WorkingPackage.GetTestFiles()) {
		if request.Metrics != nil {
			request.Metrics.TestCases[testFile.Name] = false
		}
		if err != nil {
			log.Debugf("failed to read test %s: %+v", testFile.Name, err)
			failures = append(failures, domain.TestFailure{
				Name:   testFile.Name,
				Kind:   domain.TestFailureFixture,
				Stderr: truncateForFeedback(err.Error()),
			})
			continue
		}

		if failure := cc.doTest(ctx, runner.WorkingDir(), testFile); failure != nil {
			failures = append(failures, *failure)
			log.Debugf("test %s failed (%s)", testFile.Name, failure.Kind)
			continue
		}
		if request.Metrics != nil {
			request.Metrics.TestCases[testFile.Name] = true
		}
		log.Debugf("test %s succeeded ", testFile.Name)
	}
	errCount := len(failures)
	if request.Metrics != nil {
		request.Metrics.TestTime = time.Since(startTime)
		request.Metrics.TestError = errCount
	}
	if errCount != 0 {
		// Deterministic order: the failures feed prompt evidence and map
		// iteration order above is randomized.
		sort.Slice(failures, func(i, j int) bool { return failures[i].Name < failures[j].Name })
		names := make([]string, 0, len(failures))
		for _, f := range failures {
			names = append(names, fmt.Sprintf("%s (%s)", f.Name, f.Kind))
		}
		log.Debugf("tests failed: %d/%d", errCount, len(request.WorkingPackage.TestFiles))
		return domain.NewTestingErrorWithFailures(
			fmt.Errorf("%d/%d tests failed: %s", errCount, len(request.WorkingPackage.TestFiles), strings.Join(names, ", ")),
			failures,
		)
	}
	log.Debugf("%d tests succeeded", len(request.WorkingPackage.TestFiles))
	return nil
}

// doTest runs one test case and returns nil on success, or the captured
// failure evidence (input, expected vs. actual output, stderr) so repair
// stages can see what actually went wrong instead of just a count.
func (cc *GoPackageTester) doTest(ctx context.Context, dir string, t *domain.TestFile) *domain.TestFailure {
	timeout := cc.testTimeout
	if timeout <= 0 {
		timeout = defaultTestTimeout
	}
	// Per-test timeout: a translated infinite loop (or a blocking call
	// without its own timeout) must fail this one test with actionable
	// evidence instead of hanging the single worker goroutine.
	testCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Prefer the binary goBuilder already produced (`go build -o fn .`):
	// `go run .` recompiles on every test case, multiplied by every
	// validation retry, and killing it on timeout only reaches the parent
	// process. Fall back to `go run .` for pipelines whose build step didn't
	// produce ./fn.
	var cmd *exec.Cmd
	if bin := filepath.Join(dir, "fn"); fileExists(bin) {
		cmd = exec.CommandContext(testCtx, bin)
	} else {
		cmd = exec.CommandContext(testCtx, "go", "run", ".")
	}
	// `go run` spawns the compiled binary as a child that inherits the
	// stdout/stderr pipes; killing `go run` alone leaves those pipes open
	// and cmd.Run() would keep blocking on them (i.e. a looping child would
	// still hang the worker). WaitDelay forces Run to return shortly after
	// the timeout even if the child never exits.
	cmd.WaitDelay = 2 * time.Second
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), t.Env...)
	in := strings.NewReader(t.Input)
	out := &bytes.Buffer{}
	errBuf := &bytes.Buffer{}

	cmd.Stdin = in
	cmd.Stdout = out
	cmd.Stderr = errBuf
	if err := cmd.Run(); err != nil {
		kind := domain.TestFailureError
		stderrText := strings.TrimSpace(errBuf.String() + "\n" + err.Error())
		if errors.Is(testCtx.Err(), context.DeadlineExceeded) {
			kind = domain.TestFailureTimeout
			stderrText = fmt.Sprintf("test run exceeded the %s time limit (likely an infinite loop or a blocking call without a timeout)", timeout)
		}
		return &domain.TestFailure{
			Name:     t.Name,
			Kind:     kind,
			Input:    truncateForFeedback(t.Input),
			Expected: truncateForFeedback(t.Output),
			Actual:   truncateForFeedback(out.String()),
			Stderr:   truncateForFeedback(stderrText),
		}
	}
	cleanOut := domain.MinimizeString(out.String())

	if ok, reason := cc.validateTestOutput(cleanOut, t); !ok {
		log.Debugf("test failed (%s). %s, expected:%s, errors:%s", reason, cleanOut, t.Output, errBuf.String())
		return &domain.TestFailure{
			Name:     t.Name,
			Kind:     domain.TestFailureMismatch,
			Input:    truncateForFeedback(t.Input),
			Expected: truncateForFeedback(t.Output),
			Actual:   truncateForFeedback(cleanOut),
			Stderr:   truncateForFeedback(strings.TrimSpace(errBuf.String())),
			Detail:   reason,
		}
	}

	return nil
}

// fileExists reports whether path exists as a regular file.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// truncateForFeedback caps a captured test artifact so the failure evidence
// stays prompt-sized even when a function prints large payloads.
func truncateForFeedback(s string) string {
	const maxLen = 2000
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "\n... [truncated]"
}

func (cc *GoPackageTester) validateTestOutput(testOutput string, testFile *domain.TestFile) (bool, string) {
	validator := cc.validator
	if validator == nil {
		validator = &SimilarityValidation{}
	}

	if testFile.UndeterministicResults {
		return validator.validateUndeterministic(testOutput, testFile.Output)
	}
	return validator.validate(testOutput, testFile.Output)
}

// ValidationStrategy decides whether a test run's output satisfies the
// expected value. The string return is a human/prompt-readable reason for a
// mismatch ("" on success), which flows into TestFailure.Detail.
type ValidationStrategy interface {
	validate(in, expected string) (bool, string)
	validateUndeterministic(in, expected string) (bool, string)
}

// SimilarityValidation is the last-resort fuzzy comparison, used directly
// only when no strategy is configured and as the fallback when either side
// of a structural comparison is not JSON at all.
type SimilarityValidation struct{}

// validate passes when the output is sufficiently similar to the expected
// value (overlap coefficient: 1.0 = identical, 0.0 = disjoint).
func (SimilarityValidation) validate(in, expected string) (bool, string) {
	return similarityAtLeast(in, expected, 0.9)
}

func (SimilarityValidation) validateUndeterministic(in, expected string) (bool, string) {
	return similarityAtLeast(in, expected, 0.6)
}

func similarityAtLeast(in, expected string, threshold float64) (bool, string) {
	sim := strutil.Similarity(in, expected, metrics.NewOverlapCoefficient())
	if sim >= threshold {
		return true, ""
	}
	return false, fmt.Sprintf("output similarity %.2f below threshold %.2f", sim, threshold)
}

// NewJSONStructureValidation returns the standard output validator
// (task_args strategy: "json"): a deterministic structural comparison via
// the shared compare package - strict subset matching normally, type-shape
// only for tests flagged non-deterministic - with string similarity kept
// strictly as the fallback for non-JSON outputs. The same comparator backs
// the Floci route (floci.matchOutput), so both validation paths agree on
// what "equivalent" means.
func NewJSONStructureValidation() ValidationStrategy {
	return &JSONStructureValidation{fallback: SimilarityValidation{}}
}

// JSONStructureValidation implements ValidationStrategy on top of
// compare.JSONSubset.
type JSONStructureValidation struct {
	fallback SimilarityValidation
}

func (vs *JSONStructureValidation) validate(in, expected string) (bool, string) {
	return vs.compareOutputs(in, expected, compare.Strict)
}

func (vs *JSONStructureValidation) validateUndeterministic(in, expected string) (bool, string) {
	return vs.compareOutputs(in, expected, compare.ShapeOnly)
}

func (vs *JSONStructureValidation) compareOutputs(in, expected string, mode compare.Mode) (bool, string) {
	var expectedJSON map[string]interface{}
	if err := json.Unmarshal([]byte(expected), &expectedJSON); err != nil {
		return vs.fallbackFor(mode)(in, expected)
	}

	var actualJSON map[string]interface{}
	if err := json.Unmarshal([]byte(in), &actualJSON); err != nil {
		return vs.fallbackFor(mode)(in, expected)
	}

	// unwrap the test harness envelope: {"response": ...} on success,
	// {"error": ...} when the handler itself returned an error
	if val, ok := actualJSON["error"]; ok {
		log.Debugf("handle function caused error: %s", val)
		return false, fmt.Sprintf("handler returned an error: %v", val)
	}
	actual := interface{}(actualJSON)
	if val, ok := actualJSON["response"]; ok {
		actual = val
	}

	if ok, path := compare.JSONSubset(expectedJSON, actual, mode); !ok {
		if mode == compare.ShapeOnly {
			return false, fmt.Sprintf("output structure/type mismatch at %s (values ignored for non-deterministic test)", path)
		}
		return false, fmt.Sprintf("output mismatch at %s", path)
	}
	return true, ""
}

func (vs *JSONStructureValidation) fallbackFor(mode compare.Mode) func(in, expected string) (bool, string) {
	if mode == compare.ShapeOnly {
		return vs.fallback.validateUndeterministic
	}
	return vs.fallback.validate
}
