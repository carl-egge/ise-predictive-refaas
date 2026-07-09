package pipeline

import (
	"context"
	"errors"
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
