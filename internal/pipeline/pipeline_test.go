package pipeline

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/carl-egge/ise-predictive-refaas/internal/domain"
)

// stubConverter counts invocations and returns a fixed error.
type stubConverter struct {
	calls int
	err   error
}

func (s *stubConverter) Apply(*Runner, *domain.ConversionRequest) error {
	s.calls++
	return s.err
}

// funcConverter adapts a function to the Converter interface for tests that
// need per-call behavior.
type funcConverter struct {
	fn func(*Runner, *domain.ConversionRequest) error
}

func (f *funcConverter) Apply(r *Runner, req *domain.ConversionRequest) error {
	return f.fn(r, req)
}

// TestExecuteTaskSkipsRecoveryForLLMErrors guards the F2 pipeline behavior:
// an infrastructure failure (domain.LLMError) must not trigger the recovery
// task - a recovery prompt cannot fix an API outage and would only spend
// more tokens on it.
func TestExecuteTaskSkipsRecoveryForLLMErrors(t *testing.T) {
	failing := &stubConverter{err: domain.NewLLMError(errors.New("api unavailable"))}
	recovery := &stubConverter{}

	task := &ConversionTask{
		ID:            "main",
		Execute:       failing,
		MaxRetryCount: 2,
		OnFailure:     &ConversionTask{ID: "recover", Execute: recovery, MaxRetryCount: 1},
	}
	p := NewPipeline(task)
	runner := NewRunner(context.Background(), p, nil)

	err := p.Execute(runner, &domain.ConversionRequest{})
	if err == nil {
		t.Fatal("expected the task to fail")
	}
	if failing.calls != 2 {
		t.Errorf("task executed %d times, want 2 (all retries used)", failing.calls)
	}
	if recovery.calls != 0 {
		t.Errorf("recovery ran %d times for an LLMError, want 0", recovery.calls)
	}
}

// TestExecuteTaskRecordsPerTaskMetrics guards the B5 instrumentation:
// attempts, failures and recovery activity are attributed per task id.
func TestExecuteTaskRecordsPerTaskMetrics(t *testing.T) {
	failing := &stubConverter{err: errors.New("compilation failed")}
	recovery := &stubConverter{}

	task := &ConversionTask{
		ID:            "main",
		Execute:       failing,
		MaxRetryCount: 2,
		OnFailure:     &ConversionTask{ID: "recover", Execute: recovery, MaxRetryCount: 1},
	}
	p := NewPipeline(task)
	runner := NewRunner(context.Background(), p, nil)

	req := &domain.ConversionRequest{Metrics: &domain.Metrics{TestCases: map[string]bool{}}}
	if err := p.Execute(runner, req); err == nil {
		t.Fatal("expected the task to fail")
	}

	main := req.Metrics.PerTask["main"]
	if main == nil || main.Executions != 2 || main.Failures != 2 {
		t.Errorf("PerTask[main] = %+v, want 2 executions / 2 failures", main)
	}
	rec := req.Metrics.PerTask["recover"]
	if rec == nil || rec.Executions != 1 || rec.Failures != 0 {
		t.Errorf("PerTask[recover] = %+v, want 1 execution / 0 failures", rec)
	}
}

// TestExecuteTaskRestoresCorruptedWorkingPackage guards the snapshot logic:
// when a task nils the working package and fails, the pre-attempt snapshot
// must be restored before the next attempt.
func TestExecuteTaskRestoresCorruptedWorkingPackage(t *testing.T) {
	calls := 0
	exec := &funcConverter{fn: func(_ *Runner, req *domain.ConversionRequest) error {
		calls++
		if calls == 1 {
			req.WorkingPackage = nil
			return errors.New("corrupted the working package")
		}
		if req.WorkingPackage == nil {
			return errors.New("snapshot was not restored before the retry")
		}
		return nil
	}}

	task := &ConversionTask{ID: "t", Execute: exec, MaxRetryCount: 2}
	p := NewPipeline(task)
	runner := NewRunner(context.Background(), p, nil)
	req := &domain.ConversionRequest{WorkingPackage: &domain.DeploymentPackage{RootFile: "original"}}

	if err := p.Execute(runner, req); err != nil {
		t.Fatalf("expected recovery via snapshot, got: %v", err)
	}
	if calls != 2 {
		t.Errorf("task executed %d times, want 2", calls)
	}
	if req.WorkingPackage == nil || req.WorkingPackage.RootFile != "original" {
		t.Errorf("working package not restored: %+v", req.WorkingPackage)
	}
}

// TestExecuteTaskRetriesOnValidationFailure documents the validation-retry
// semantics: a failing validation re-executes the same task (sharing the
// retry budget) and ultimately returns the validation error.
func TestExecuteTaskRetriesOnValidationFailure(t *testing.T) {
	execCalls := 0
	exec := &funcConverter{fn: func(*Runner, *domain.ConversionRequest) error {
		execCalls++
		return nil
	}}
	valCalls := 0
	validation := &funcConverter{fn: func(*Runner, *domain.ConversionRequest) error {
		valCalls++
		return errors.New("tests failed")
	}}

	task := &ConversionTask{ID: "t", Execute: exec, Validation: validation, MaxRetryCount: 2}
	p := NewPipeline(task)
	runner := NewRunner(context.Background(), p, nil)

	err := p.Execute(runner, &domain.ConversionRequest{})
	if err == nil || err.Error() != "tests failed" {
		t.Fatalf("expected the validation error, got: %v", err)
	}
	if execCalls != 2 {
		t.Errorf("Execute ran %d times, want 2 (validation retries share the budget)", execCalls)
	}
	if valCalls != 3 {
		t.Errorf("Validation ran %d times, want 3", valCalls)
	}
}

// TestExecuteTaskRecoveryRunsBetweenAttempts documents the recovery order:
// the recovery task runs between attempts of the failing task, never after
// the final one.
func TestExecuteTaskRecoveryRunsBetweenAttempts(t *testing.T) {
	var order []string
	failing := &funcConverter{fn: func(*Runner, *domain.ConversionRequest) error {
		order = append(order, "main")
		return errors.New("still broken")
	}}
	recovery := &funcConverter{fn: func(*Runner, *domain.ConversionRequest) error {
		order = append(order, "recover")
		return nil
	}}

	task := &ConversionTask{
		ID:            "main",
		Execute:       failing,
		MaxRetryCount: 3,
		OnFailure:     &ConversionTask{ID: "recover", Execute: recovery, MaxRetryCount: 1},
	}
	p := NewPipeline(task)
	runner := NewRunner(context.Background(), p, nil)

	if err := p.Execute(runner, &domain.ConversionRequest{}); err == nil {
		t.Fatal("expected the task to fail")
	}
	want := []string{"main", "recover", "main", "recover", "main"}
	if len(order) != len(want) {
		t.Fatalf("invocation order = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("invocation order = %v, want %v", order, want)
		}
	}
}

// TestExecuteTaskTracksCurrentAttempt guards [E3]: a converter needs to be
// able to tell a fresh execution from a resample-style retry of the same
// task (e.g. to opt into a temperature bump), via
// ConversionRequest.CurrentAttempt - 1-based, incrementing on every
// execution of this task.
func TestExecuteTaskTracksCurrentAttempt(t *testing.T) {
	var attempts []int
	failing := &funcConverter{fn: func(_ *Runner, req *domain.ConversionRequest) error {
		attempts = append(attempts, req.CurrentAttempt)
		return errors.New("still broken")
	}}

	task := &ConversionTask{ID: "main", Execute: failing, MaxRetryCount: 3}
	p := NewPipeline(task)
	runner := NewRunner(context.Background(), p, nil)

	if err := p.Execute(runner, &domain.ConversionRequest{}); err == nil {
		t.Fatal("expected the task to fail")
	}
	want := []int{1, 2, 3}
	if len(attempts) != len(want) {
		t.Fatalf("attempts = %v, want %v", attempts, want)
	}
	for i := range want {
		if attempts[i] != want[i] {
			t.Fatalf("attempts = %v, want %v", attempts, want)
		}
	}
}

// TestExecuteTaskJoinsOriginalErrorWithFailedRecovery guards [B3]: when a
// recovery task itself fails, the returned/last-recorded error must still
// surface the original defect (what the fixer/align prompt's {{ .issue }}
// needs) instead of only "recovery also failed", and a typed error on either
// side must still be reachable via errors.As.
func TestExecuteTaskJoinsOriginalErrorWithFailedRecovery(t *testing.T) {
	originalErr := domain.NewCompilationError(errors.New("undefined: foo"))
	failing := &stubConverter{err: originalErr}
	recovery := &stubConverter{err: errors.New("recovery prompt errored")}

	task := &ConversionTask{
		ID:            "main",
		Execute:       failing,
		MaxRetryCount: 2,
		OnFailure:     &ConversionTask{ID: "recover", Execute: recovery, MaxRetryCount: 1},
	}
	p := NewPipeline(task)
	runner := NewRunner(context.Background(), p, nil)

	req := &domain.ConversionRequest{}
	err := p.Execute(runner, req)
	if err == nil {
		t.Fatal("expected the task to fail")
	}

	if !strings.Contains(err.Error(), "undefined: foo") {
		t.Errorf("returned error = %q, want it to still contain the original defect", err.Error())
	}
	if !strings.Contains(err.Error(), "recovery prompt errored") {
		t.Errorf("returned error = %q, want it to also contain the recovery failure", err.Error())
	}

	var ce domain.CompilationError
	if !errors.As(err, &ce) {
		t.Errorf("errors.As found no CompilationError in %v, want the original typed error reachable", err)
	}

	last := req.LastError()
	if last == nil || !strings.Contains(last.Error(), "undefined: foo") {
		t.Errorf("LastError() = %v, want it to still contain the original defect", last)
	}
}

// TestExecuteTaskAbortsWhenCancelled verifies a cancelled job stops at task
// entry instead of spending build/test/LLM resources.
func TestExecuteTaskAbortsWhenCancelled(t *testing.T) {
	execCalls := 0
	exec := &funcConverter{fn: func(*Runner, *domain.ConversionRequest) error {
		execCalls++
		return nil
	}}

	task := &ConversionTask{ID: "t", Execute: exec, MaxRetryCount: 2}
	p := NewPipeline(task)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runner := NewRunner(ctx, p, nil)

	err := p.Execute(runner, &domain.ConversionRequest{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got: %v", err)
	}
	if execCalls != 0 {
		t.Errorf("a cancelled job must not execute tasks, ran %d times", execCalls)
	}
}

// TestExecuteTaskSharedRecoveryGetsFreshBudgetPerInvocation pins a
// non-obvious, easy-to-"fix"-into-a-bug property of the retry engine,
// prompted by a real run (evaluation_set/f6 against
// scripts/chatai-devstral-summary.json) that repeatedly logged
// "running task (testRecovery) with (1 / 3) executions": a task whose
// Execute succeeds via `break` never advances its own RetryCount (`break`
// skips a for-loop's post-statement, unlike `continue`), so a task with no
// Validation step keeps a fresh MaxRetryCount budget every time it is
// invoked afresh - e.g. as a shared OnFailure target reached from a parent's
// own repeated retries, exactly like testRecovery/gollmRecovery in
// default.json.
//
// This is intentional, not a bug: default.json's "gollmRecovery" is shared
// as the recovery target of BOTH "builder" and "testRecoveryBuild". If
// RetryCount instead accumulated as a lifetime total across the whole
// conversion, builder's own failures could exhaust gollmRecovery's budget
// before testRecoveryBuild ever got a turn. The outer parent's own
// MaxRetryCount (goTester's 5 in the real config) is what actually bounds
// the total cost - see TODO.md [C5] for the (separate, still-open) concern
// that this bound can still mean a lot of wasted LLM calls when recovery
// keeps "succeeding" locally without fixing the real failure.
func TestExecuteTaskSharedRecoveryGetsFreshBudgetPerInvocation(t *testing.T) {
	recoveryRuns := 0
	recovery := &funcConverter{fn: func(*Runner, *domain.ConversionRequest) error {
		recoveryRuns++
		return nil // "succeeds" every time, like realign producing buildable code
	}}
	recoveryTask := &ConversionTask{ID: "recover", Execute: recovery, MaxRetryCount: 3}

	mainRuns := 0
	failing := &funcConverter{fn: func(*Runner, *domain.ConversionRequest) error {
		mainRuns++
		// Varies per attempt so the [C5] stagnation guard (tested
		// separately below) never fires here - this test is purely about
		// budget sharing, not stagnation detection.
		return fmt.Errorf("test keeps failing (attempt %d)", mainRuns)
	}}
	main := &ConversionTask{ID: "main", Execute: failing, MaxRetryCount: 5, OnFailure: recoveryTask}

	p := NewPipeline(main)
	runner := NewRunner(context.Background(), p, nil)

	err := p.Execute(runner, &domain.ConversionRequest{})
	if err == nil {
		t.Fatal("expected the task to fail once its own budget is exhausted")
	}
	// bounded by main's own MaxRetryCount, not infinite
	if mainRuns != 5 {
		t.Errorf("main ran %d times, want exactly 5 (its own MaxRetryCount)", mainRuns)
	}
	// recovery invoked once per main failure that still has budget left
	// (main's last, 5th attempt fails without triggering another recovery
	// call - see executeTask's `task.RetryCount+1 < task.MaxRetryCount` guard)
	if recoveryRuns != 4 {
		t.Errorf("recovery ran %d times, want 4 (once per main retry, not blocked by its own maxRetryCount=3 despite running more than 3 times total)", recoveryRuns)
	}
	if recoveryTask.RetryCount != 0 {
		t.Errorf("recovery's own RetryCount = %d, want 0 (break-on-success never advances it, which is what lets a shared recovery target keep getting invoked)", recoveryTask.RetryCount)
	}
}

// TestExecuteTaskStagnationAbortsBeforeExhaustingRetryBudget guards [C5]: the
// exact scenario observed running evaluation_set/f6 against
// scripts/chatai-devstral-summary.json, where a recovery task keeps
// "succeeding" locally without ever changing the parent's failure. Without
// the guard this would run until main's own MaxRetryCount (5 here); with it,
// the loop must give up early once the same failure text has recurred
// stagnationAbortThreshold times in a row, and it must flag {{ .stagnant }}
// for the recovery prompt one occurrence before that.
func TestExecuteTaskStagnationAbortsBeforeExhaustingRetryBudget(t *testing.T) {
	recoveryRuns := 0
	stagnantAtRun := map[int]bool{}
	recovery := &funcConverter{fn: func(_ *Runner, req *domain.ConversionRequest) error {
		recoveryRuns++
		stagnantAtRun[recoveryRuns] = req.Metadata["stagnant"] == "true"
		return nil
	}}
	recoveryTask := &ConversionTask{ID: "recover", Execute: recovery, MaxRetryCount: 3}

	mainRuns := 0
	failing := &funcConverter{fn: func(*Runner, *domain.ConversionRequest) error {
		mainRuns++
		return errors.New("identical failure every time") // never changes
	}}
	main := &ConversionTask{ID: "main", Execute: failing, MaxRetryCount: 5, OnFailure: recoveryTask}

	p := NewPipeline(main)
	runner := NewRunner(context.Background(), p, nil)

	req := &domain.ConversionRequest{}
	err := p.Execute(runner, req)
	if err == nil {
		t.Fatal("expected the task to fail")
	}
	if !strings.Contains(err.Error(), "no progress") {
		t.Errorf("error should explain the stagnation abort, got: %v", err)
	}

	// aborts on the 3rd identical failure, i.e. after only 2 recovery calls -
	// well before main's own budget of 5 would have been exhausted
	if mainRuns != 3 {
		t.Errorf("main ran %d times, want exactly 3 (aborted by the stagnation guard, not exhausting its own MaxRetryCount=5)", mainRuns)
	}
	if recoveryRuns != 2 {
		t.Errorf("recovery ran %d times, want exactly 2 (one nudged attempt after the first repeat, then abort instead of a 3rd)", recoveryRuns)
	}
	if stagnantAtRun[1] {
		t.Error("the first recovery call must not see the stagnant flag - nothing had repeated yet")
	}
	if !stagnantAtRun[2] {
		t.Error("the second recovery call (after the first repeat) must see {{ .stagnant }} so the prompt can try something different")
	}
	if req.Metadata["stagnant"] != "true" {
		t.Errorf("stagnant flag should still be set on the request after the abort, got metadata: %v", req.Metadata)
	}
}

// TestExecuteTaskStagnationResetsOnDifferentFailure verifies genuine
// per-attempt progress (a different failure text each time) never trips the
// guard, however many times the parent retries.
func TestExecuteTaskStagnationResetsOnDifferentFailure(t *testing.T) {
	recoveryRuns := 0
	recovery := &funcConverter{fn: func(*Runner, *domain.ConversionRequest) error {
		recoveryRuns++
		return nil
	}}
	recoveryTask := &ConversionTask{ID: "recover", Execute: recovery, MaxRetryCount: 3}

	mainRuns := 0
	failing := &funcConverter{fn: func(*Runner, *domain.ConversionRequest) error {
		mainRuns++
		return fmt.Errorf("a different failure each time: %d", mainRuns)
	}}
	main := &ConversionTask{ID: "main", Execute: failing, MaxRetryCount: 5, OnFailure: recoveryTask}

	p := NewPipeline(main)
	runner := NewRunner(context.Background(), p, nil)
	req := &domain.ConversionRequest{}

	if err := p.Execute(runner, req); err == nil {
		t.Fatal("expected the task to fail")
	}
	if mainRuns != 5 {
		t.Errorf("main ran %d times, want its full budget of 5 - a genuinely changing failure must never trigger the stagnation guard", mainRuns)
	}
	if req.Metadata["stagnant"] == "true" {
		t.Error("stagnant flag must not be set when the failure text keeps changing")
	}
}

// TestExecuteTaskRunsRecoveryForOrdinaryErrors is the counterpart: a normal
// failure (e.g. a build error) still routes through the recovery task.
func TestExecuteTaskRunsRecoveryForOrdinaryErrors(t *testing.T) {
	failing := &stubConverter{err: errors.New("compilation failed")}
	recovery := &stubConverter{}

	task := &ConversionTask{
		ID:            "main",
		Execute:       failing,
		MaxRetryCount: 2,
		OnFailure:     &ConversionTask{ID: "recover", Execute: recovery, MaxRetryCount: 1},
	}
	p := NewPipeline(task)
	runner := NewRunner(context.Background(), p, nil)

	err := p.Execute(runner, &domain.ConversionRequest{})
	if err == nil {
		t.Fatal("expected the task to fail")
	}
	if recovery.calls != 1 {
		t.Errorf("recovery ran %d times for an ordinary error, want 1", recovery.calls)
	}
	if failing.calls != 2 {
		t.Errorf("task executed %d times, want 2", failing.calls)
	}
}
