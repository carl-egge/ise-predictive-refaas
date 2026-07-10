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
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/carl-egge/ise-predictive-refaas/internal/compare"
	"github.com/carl-egge/ise-predictive-refaas/internal/domain"
	"github.com/carl-egge/ise-predictive-refaas/internal/fixture"
	"github.com/carl-egge/ise-predictive-refaas/internal/pipeline"
	log "github.com/sirupsen/logrus"
)

// defaultTestTimeout bounds a single test-case run of the translated
// program. Generous enough for `go run`'s cached rebuild plus a network
// call, but a translated infinite loop no longer hangs the single worker.
const defaultTestTimeout = 30 * time.Second

// GoPackageTester runs the package in the working directory and validates its
// stdout against the expected test outputs. Fixtures are parsed through the
// canonical schema (internal/fixture: payload/expectedOutput/outputMode, with
// legacy input/output fixtures lowered automatically) and outputs are judged
// by the shared comparator (fixture.MatchOutput) - the same definition of
// "equivalent" the Floci route uses. Cases that declare setup/sideEffects run
// like any other, but their side-effect assertions are ignored with a warning:
// only the flociTester can execute those.
type GoPackageTester struct {
	// testTimeout bounds each individual test-case run (task_args
	// "test_timeout", default defaultTestTimeout).
	testTimeout time.Duration
}

func init() {
	pipeline.RegisterConverterFactory("goTester", NewGoPackageTester)
}

// NewGoPackageTester constructs a tester with an optional per-test timeout
// from args.
func NewGoPackageTester(args map[string]interface{}) pipeline.Converter {
	if kind, ok := args["strategy"].(string); ok {
		// There is exactly one comparator now (fixture.MatchOutput); the
		// per-fixture outputMode replaced the task-level strategy switch.
		log.Debugf("goTester: strategy %q is deprecated and ignored; comparison is per-fixture via outputMode", kind)
	}
	return &GoPackageTester{
		testTimeout: parseTimeout(args, "test_timeout", defaultTestTimeout),
	}
}

// Apply runs every test fixture in the runner's working directory and updates
// the request's metrics and error state.
func (cc *GoPackageTester) Apply(runner *pipeline.Runner, request *domain.ConversionRequest) error {
	if request.WorkingPackage == nil {
		log.Errorf("missing working package for %s", request.Id)
		return fmt.Errorf("the working package is required")
	}

	if runner.WorkingDir() == "" {
		// Without a working dir, cmd.Dir is "" below and `go run .`/./fn
		// silently executes in the refaas process's own CWD instead of the
		// translated package - a goBuilder task must run first to produce
		// one. Fail loudly instead of testing whatever happens to be in the
		// service's directory.
		return fmt.Errorf("goTester (task %q) has no working directory - a goBuilder task must run before it in the pipeline", request.CurrentTask)
	}

	if request.SourcePackage != nil && (len(request.SourcePackage.TestFiles)) > len(request.WorkingPackage.TestFiles) {
		request.WorkingPackage.TestFiles = make(map[string]string)
		maps.Copy(request.WorkingPackage.TestFiles, request.SourcePackage.TestFiles)
		log.Debugf("Recoverting WP Tests")
	}

	startTime := time.Now()
	ctx := runner
	pkg := request.WorkingPackage
	failures := make([]domain.TestFailure, 0)
	log.Debugf("Running GoPackageTester with %d tests", len(pkg.TestFiles))
	// Parse per file (rather than fixture.FromPackage) so one broken fixture
	// becomes a failure entry while the remaining cases still run.
	for _, fileName := range slices.Sorted(maps.Keys(pkg.TestFiles)) {
		tc, err := fixture.Parse(fileName, []byte(pkg.TestFiles[fileName]))
		if err != nil {
			log.Debugf("failed to read test %s: %+v", fileName, err)
			if request.Metrics != nil {
				request.Metrics.TestCases[fileName] = false
			}
			failures = append(failures, domain.TestFailure{
				Name:   fileName,
				Kind:   domain.TestFailureFixture,
				Stderr: truncateForFeedback(err.Error()),
			})
			continue
		}
		if request.Metrics != nil {
			request.Metrics.TestCases[tc.Name] = false
		}
		if tc.HasSideEffects() {
			log.Warnf("test case %q declares setup/sideEffects, which goTester cannot execute; only the payload/expectedOutput half is validated here (use the flociTester stage for side-effect assertions)", tc.Name)
		}

		if failure := cc.doTest(ctx, runner.WorkingDir(), tc, pkg.Env); failure != nil {
			failures = append(failures, *failure)
			log.Debugf("test %s failed (%s)", tc.Name, failure.Kind)
			continue
		}
		if request.Metrics != nil {
			request.Metrics.TestCases[tc.Name] = true
		}
		log.Debugf("test %s succeeded ", tc.Name)
	}
	errCount := len(failures)
	if request.Metrics != nil {
		request.Metrics.TestTime = time.Since(startTime)
		request.Metrics.TestError = errCount
	}
	if errCount != 0 {
		// Deterministic order: the failures feed prompt evidence.
		sort.Slice(failures, func(i, j int) bool { return failures[i].Name < failures[j].Name })
		names := make([]string, 0, len(failures))
		for _, f := range failures {
			names = append(names, fmt.Sprintf("%s (%s)", f.Name, f.Kind))
		}
		log.Debugf("tests failed: %d/%d", errCount, len(pkg.TestFiles))
		return domain.NewTestingErrorWithFailures(
			fmt.Errorf("%d/%d tests failed: %s", errCount, len(pkg.TestFiles), strings.Join(names, ", ")),
			failures,
		)
	}
	log.Debugf("%d tests succeeded", len(pkg.TestFiles))
	return nil
}

// doTest runs one test case and returns nil on success, or the captured
// failure evidence (input, expected vs. actual output, stderr) so repair
// stages can see what actually went wrong instead of just a count.
func (cc *GoPackageTester) doTest(ctx context.Context, dir string, tc fixture.TestCase, pkgEnv []string) *domain.TestFailure {
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
	// Package-level env first, the fixture's own entries last: exec.Cmd keeps
	// the last value for duplicate keys, so per-test overrides win.
	cmd.Env = append(append(os.Environ(), pkgEnv...), tc.Env...)
	payload := string(tc.Payload)
	if strings.TrimSpace(payload) == "" {
		payload = "{}" // same default event the Floci route invokes with
	}
	expected := string(tc.ExpectedOutput)
	in := strings.NewReader(payload)
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
			Name:     tc.Name,
			Kind:     kind,
			Input:    truncateForFeedback(payload),
			Expected: truncateForFeedback(expected),
			Actual:   truncateForFeedback(out.String()),
			Stderr:   truncateForFeedback(stderrText),
		}
	}
	cleanOut := domain.MinimizeString(out.String())

	if ok, reason := validateHarnessOutput(tc.ExpectedOutput, cleanOut, tc.CompareMode()); !ok {
		log.Debugf("test failed (%s). %s, expected:%s, errors:%s", reason, cleanOut, expected, errBuf.String())
		return &domain.TestFailure{
			Name:     tc.Name,
			Kind:     domain.TestFailureMismatch,
			Input:    truncateForFeedback(payload),
			Expected: truncateForFeedback(expected),
			Actual:   truncateForFeedback(cleanOut),
			Stderr:   truncateForFeedback(strings.TrimSpace(errBuf.String())),
			Detail:   reason,
		}
	}

	return nil
}

// validateHarnessOutput unwraps the test harness envelope ({"response": ...}
// on success, {"error": ...} when the handler itself returned an error) from
// the program's stdout and judges the payload against the fixture's expected
// output via the shared comparator. An empty expectation skips output
// validation (side-effect-only cases). Non-envelope stdout is compared as-is,
// with MatchOutput's substring fallback covering non-JSON output.
func validateHarnessOutput(expected json.RawMessage, stdout string, mode compare.Mode) (bool, string) {
	if len(expected) == 0 {
		return true, ""
	}
	actual := []byte(stdout)
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(actual, &envelope); err == nil {
		if errVal, ok := envelope["error"]; ok {
			log.Debugf("handle function caused error: %s", errVal)
			return false, fmt.Sprintf("handler returned an error: %s", errVal)
		}
		if resp, ok := envelope["response"]; ok {
			actual = resp
		}
	}
	if err := fixture.MatchOutput(expected, actual, mode); err != nil {
		if mode == compare.ShapeOnly {
			return false, fmt.Sprintf("%s (values ignored for non-deterministic test)", err)
		}
		return false, err.Error()
	}
	return true, ""
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
