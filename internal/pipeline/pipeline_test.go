package pipeline

import (
	"context"
	"errors"
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
